package agentcore

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/sched"
)

const (
	CatchUpWindow = 6 * time.Hour // se o agente estava parado, ainda roda um backup atrasado até esse limite
	tickEvery     = 15 * time.Second
	maxLogLines   = 2000
)

type Config struct {
	HostRoot  string // raiz do servidor vista pelo agente ("/" fora de contêiner; "/host" com o host montado)
	BackupDir string // onde os arquivos de backup são gravados (nunca fora dela)
}

// Executor roda um job. Trocável nos testes.
type Executor func(ctx context.Context, job FilesJob, loc *time.Location, now time.Time, h Hooks) (Result, error)

type runReq struct {
	spec         *pb.JobSpec
	uid          string
	trigger      string
	scheduledFor time.Time
}

// Core é o "cérebro" do agente: agenda, executa um backup por vez e relata ao Server (quando conectado).
type Core struct {
	cfg   Config
	st    *State
	log   *slog.Logger
	now   func() time.Time
	exec  Executor
	queue chan runReq

	mu       sync.Mutex
	jobs     map[int64]*pb.JobSpec
	lastFire map[int64]int64
	running  map[string]context.CancelFunc // por run_uid
	busyJobs map[int64]bool                // jobs enfileirados ou rodando
	send     func(*pb.AgentMessage) bool   // nil quando desconectado
}

func New(cfg Config, st *State, log *slog.Logger) *Core {
	c := &Core{cfg: cfg, st: st, log: log, now: time.Now, queue: make(chan runReq, 64),
		jobs: map[int64]*pb.JobSpec{}, lastFire: st.LoadLastFire(), running: map[string]context.CancelFunc{}, busyJobs: map[int64]bool{}}
	for _, j := range st.LoadJobs() {
		c.jobs[j.JobId] = j
	}
	c.exec = func(ctx context.Context, job FilesJob, loc *time.Location, now time.Time, h Hooks) (Result, error) {
		return RunFiles(ctx, job, c.cfg.HostRoot, c.cfg.BackupDir, loc, now, h)
	}
	return c
}

// SetSender liga/desliga o canal com o Server (nil = offline: os resultados vão para a fila local).
func (c *Core) SetSender(f func(*pb.AgentMessage) bool) {
	c.mu.Lock()
	c.send = f
	c.mu.Unlock()
}

func (c *Core) trySend(m *pb.AgentMessage) bool {
	c.mu.Lock()
	f := c.send
	c.mu.Unlock()
	return f != nil && f(m)
}

// SyncJobs substitui o conjunto de jobs (snapshot enviado pelo Server).
func (c *Core) SyncJobs(specs []*pb.JobSpec) {
	c.mu.Lock()
	next := make(map[int64]*pb.JobSpec, len(specs))
	now := c.now().Unix()
	for _, s := range specs {
		next[s.JobId] = s
		if _, known := c.lastFire[s.JobId]; !known {
			c.lastFire[s.JobId] = now // job novo: não cobra horários anteriores a ele
		}
	}
	c.jobs = next
	for id := range c.lastFire {
		if _, ok := next[id]; !ok {
			delete(c.lastFire, id)
		}
	}
	lf := copyMap(c.lastFire)
	c.mu.Unlock()
	if err := c.st.SaveJobs(specs); err != nil {
		c.log.Warn("não foi possível gravar os jobs localmente", "erro", err.Error())
	}
	_ = c.st.SaveLastFire(lf)
	c.log.Info("jobs sincronizados", "quantidade", len(specs))
}

func copyMap(m map[int64]int64) map[int64]int64 {
	o := make(map[int64]int64, len(m))
	for k, v := range m {
		o[k] = v
	}
	return o
}

// Tick dispara os jobs cujo horário chegou (ou que ficaram para trás dentro da janela de recuperação).
func (c *Core) Tick() {
	now := c.now()
	c.mu.Lock()
	var due []runReq
	changed := false
	for id, spec := range c.jobs {
		if !spec.Enabled || spec.Kind != "files" {
			continue
		}
		sc, err := sched.Parse(spec.Cron, spec.Timezone)
		if err != nil {
			continue
		}
		last := time.Unix(c.lastFire[id], 0)
		prev := sc.Prev(last, now)
		if prev.IsZero() {
			continue
		}
		c.lastFire[id] = prev.Unix()
		changed = true
		if now.Sub(prev) > CatchUpWindow {
			c.log.Warn("horário muito antigo, não será recuperado", "job", spec.Name, "previsto", prev.Format(time.RFC3339))
			continue
		}
		due = append(due, runReq{spec: spec, uid: auth.NewUUID(), trigger: "schedule", scheduledFor: prev})
	}
	lf := copyMap(c.lastFire)
	c.mu.Unlock()
	if changed {
		_ = c.st.SaveLastFire(lf)
	}
	for _, r := range due {
		c.Enqueue(r.spec.JobId, r.uid, r.trigger, r.scheduledFor)
	}
}

// Enqueue coloca um job na fila (um backup por vez). Manuais usam o run_uid criado pelo Server.
func (c *Core) Enqueue(jobID int64, uid, trigger string, scheduledFor time.Time) {
	c.mu.Lock()
	spec := c.jobs[jobID]
	if spec == nil {
		c.mu.Unlock()
		c.log.Warn("job desconhecido, ignorado", "job", jobID)
		return
	}
	if c.busyJobs[jobID] {
		c.mu.Unlock()
		c.finishSkipped(spec, uid, trigger, scheduledFor, "pulado: a execução anterior deste job ainda não terminou")
		return
	}
	c.busyJobs[jobID] = true
	c.mu.Unlock()
	select {
	case c.queue <- runReq{spec: spec, uid: uid, trigger: trigger, scheduledFor: scheduledFor}:
	default:
		c.mu.Lock()
		delete(c.busyJobs, jobID)
		c.mu.Unlock()
		c.finishSkipped(spec, uid, trigger, scheduledFor, "pulado: fila de backups cheia")
	}
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func (c *Core) finishSkipped(spec *pb.JobSpec, uid, trigger string, sf time.Time, why string) {
	n := c.now()
	rep := &pb.RunReport{RunUid: uid, JobId: spec.JobId, State: pb.RunReport_FINISHED, Trigger: trigger, ScheduledForMs: ms(sf),
		StartedMs: ms(n), FinishedMs: ms(n), Status: "failed", Error: why, JobVersion: spec.Version,
		Logs: []*pb.LogLine{{Seq: 1, UnixMs: ms(n), Level: "error", Text: why}}}
	c.deliver(rep)
}

// deliver grava na fila local e tenta enviar; a remoção acontece quando o Server confirma (ReportAck).
func (c *Core) deliver(rep *pb.RunReport) {
	if err := c.st.SpoolAdd(rep); err != nil {
		c.log.Error("não foi possível gravar o resultado na fila local", "erro", err.Error())
	}
	c.trySend(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: rep}})
}

func (c *Core) Ack(uid string) { c.st.SpoolRemove(uid) }

// Replay reenvia o que ficou na fila local (chamado ao conectar e periodicamente).
func (c *Core) Replay() {
	for _, r := range c.st.SpoolList() {
		if !c.trySend(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: r}}) {
			return
		}
	}
}

func (c *Core) Cancel(uid string) {
	c.mu.Lock()
	f := c.running[uid]
	c.mu.Unlock()
	if f != nil {
		f()
	}
}

// Run consome a fila (um backup por vez) e agenda o Tick; bloqueia até ctx terminar.
func (c *Core) Run(ctx context.Context) {
	go func() {
		t := time.NewTicker(tickEvery)
		defer t.Stop()
		c.Tick()
		n := 0
		for {
			select {
			case <-t.C:
				c.Tick()
				if n++; n%4 == 0 { // ~1 min: tenta de novo o que não foi confirmado
					c.Replay()
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		select {
		case r := <-c.queue:
			c.execute(ctx, r)
		case <-ctx.Done():
			return
		}
	}
}

func (c *Core) execute(parent context.Context, r runReq) {
	ctx, cancel := context.WithCancel(parent)
	c.mu.Lock()
	c.running[r.uid] = cancel
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		delete(c.running, r.uid)
		delete(c.busyJobs, r.spec.JobId)
		c.mu.Unlock()
	}()

	started := c.now()
	var logs []*pb.LogLine
	seq := 0
	var lmu sync.Mutex
	logf := func(level, f string, a ...any) {
		lmu.Lock()
		defer lmu.Unlock()
		if seq >= maxLogLines {
			return
		}
		seq++
		txt := fmt.Sprintf(f, a...)
		if seq == maxLogLines {
			txt = "(log truncado: limite de linhas atingido)"
		}
		logs = append(logs, &pb.LogLine{Seq: int32(seq), UnixMs: ms(c.now()), Level: level, Text: txt})
		c.log.Info(txt, "job", r.spec.Name, "nível", level)
	}
	base := &pb.RunReport{RunUid: r.uid, JobId: r.spec.JobId, Trigger: r.trigger, ScheduledForMs: ms(r.scheduledFor), StartedMs: ms(started), JobVersion: r.spec.Version}
	start := proto.Clone(base).(*pb.RunReport)
	start.State = pb.RunReport_STARTED
	c.trySend(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: start}})

	logf("info", "iniciando o job %q (%s)", r.spec.Name, r.trigger)
	loc, err := time.LoadLocation(r.spec.Timezone)
	if err != nil {
		loc = time.UTC
	}
	job := FilesJob{Name: r.spec.Name, Paths: r.spec.Paths, Excludes: r.spec.Excludes, Subdir: r.spec.DestSubdir, Keep: int(r.spec.Keep)}
	res, err := c.exec(ctx, job, loc, started, Hooks{Logf: logf, Progress: func(p Result) {
		pr := proto.Clone(base).(*pb.RunReport)
		pr.State, pr.BytesSource, pr.BytesOutput, pr.Files = pb.RunReport_PROGRESS, p.BytesSource, p.BytesOutput, p.Files
		c.trySend(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: pr}})
	}})

	fin := proto.Clone(base).(*pb.RunReport)
	fin.State, fin.FinishedMs = pb.RunReport_FINISHED, ms(c.now())
	fin.BytesSource, fin.BytesOutput, fin.Files, fin.Sha256, fin.OutputName = res.BytesSource, res.BytesOutput, res.Files, res.SHA256, res.OutputName
	switch {
	case err != nil && ctx.Err() != nil && parent.Err() == nil:
		fin.Status, fin.Error = "canceled", "cancelado pelo usuário"
		logf("warn", "backup cancelado")
	case err != nil && parent.Err() != nil:
		return // o agente está encerrando: sem relatório; o próximo início recupera pelo horário (se dentro da janela)
	case err != nil:
		fin.Status, fin.Error = "failed", err.Error()
		logf("error", "falhou: %v", err)
	case res.Warnings > 0:
		fin.Status = "warning"
		logf("warn", "concluído com %d aviso(s)", res.Warnings)
	default:
		fin.Status = "ok"
		logf("info", "concluído com sucesso em %s", c.now().Sub(started).Round(time.Second))
	}
	lmu.Lock()
	fin.Logs = logs
	lmu.Unlock()
	c.deliver(fin)
}
