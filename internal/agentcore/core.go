package agentcore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/dockerapi"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/sched"
	"github.com/AprendendoLinux/corujabackup/internal/uploader"
)

const (
	CatchUpWindow = 6 * time.Hour // se o agente estava parado, ainda roda um backup atrasado até esse limite
	tickEvery     = 15 * time.Second
	maxLogLines   = 2000
)

type Config struct {
	HostRoot  string // raiz do servidor vista pelo agente ("/" fora de contêiner; "/host" com o host montado)
	BackupDir string // onde os arquivos de backup são gravados (nunca fora dela)
	// Defesa em profundidade: o agente só executa o que a política local permitir, mesmo que o Server (ou quem o controla)
	// peça outra coisa. Vazio = sem restrição.
	AllowedPaths      []string // pastas que podem ser salvas (e tudo dentro delas)
	AllowedContainers []string // contêineres de banco em que o dump pode rodar (padrões com * e ?)
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
	active   map[string]bool               // run_uid enfileirados ou rodando (vai no Hello)
	actions  map[string]bool               // ações posteriores em andamento (tipo:run_uid)
	send     func(*pb.AgentMessage) bool   // nil quando desconectado

	docker  *dockerapi.Client
	upload  UploadFn
	waiters map[string]chan *pb.ServerMessage
	httpc   *httpClientHolder
}

type httpClientHolder struct{ opt uploader.Options }

// CloudInfo é o resultado do envio à nuvem já verificado pelo Server.
type CloudInfo struct {
	Path       string
	Hash       string
	VerifiedBy string
	Retention  []string
}

// UploadFn envia o arquivo à nuvem (trocável nos testes). progress recebe 0..100.
type UploadFn func(ctx context.Context, spec *pb.JobSpec, runUID string, res Result, h Hooks, progress func(pct int)) (CloudInfo, error)

func New(cfg Config, st *State, log *slog.Logger) *Core {
	c := &Core{cfg: cfg, st: st, log: log, now: time.Now, queue: make(chan runReq, 64),
		jobs: map[int64]*pb.JobSpec{}, lastFire: st.LoadLastFire(), running: map[string]context.CancelFunc{}, busyJobs: map[int64]bool{},
		active: map[string]bool{}, actions: map[string]bool{}, docker: dockerapi.New(os.Getenv("CORUJA_DOCKER_SOCKET")), waiters: map[string]chan *pb.ServerMessage{}, httpc: &httpClientHolder{}}
	c.upload = c.uploadToCloud
	for _, j := range st.LoadJobs() {
		c.jobs[j.JobId] = j
	}
	c.exec = func(ctx context.Context, job FilesJob, loc *time.Location, now time.Time, h Hooks) (Result, error) {
		if job.DB != nil {
			return RunDatabase(ctx, job.Name, job.DB, job.Subdir, c.docker, c.cfg.BackupDir, loc, now, h)
		}
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
		if !spec.Enabled || (spec.Kind != "files" && spec.Kind != "database") {
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
	c.active[uid] = true
	c.mu.Unlock()
	select {
	case c.queue <- runReq{spec: spec, uid: uid, trigger: trigger, scheduledFor: scheduledFor}:
	default:
		c.mu.Lock()
		delete(c.busyJobs, jobID)
		delete(c.active, uid)
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
		delete(c.active, r.uid)
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
	flushed := 0
	// envia as linhas novas do log a cada 2 s para a interface mostrar o andamento ao vivo
	flushDone := make(chan struct{})
	flushCtx, stopFlush := context.WithCancel(ctx)
	defer stopFlush()
	base := &pb.RunReport{RunUid: r.uid, JobId: r.spec.JobId, Trigger: r.trigger, ScheduledForMs: ms(r.scheduledFor), StartedMs: ms(started), JobVersion: r.spec.Version}
	start := proto.Clone(base).(*pb.RunReport)
	start.State = pb.RunReport_STARTED
	c.trySend(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: start}})

	go func() {
		defer close(flushDone)
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				lmu.Lock()
				fresh := logs[flushed:]
				flushed = len(logs)
				lmu.Unlock()
				if len(fresh) > 0 {
					pr := proto.Clone(base).(*pb.RunReport)
					pr.State, pr.Logs = pb.RunReport_PROGRESS, append([]*pb.LogLine(nil), fresh...)
					c.trySend(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: pr}})
				}
			case <-flushCtx.Done():
				return
			}
		}
	}()
	logf("info", "iniciando o job %q (%s)", r.spec.Name, r.trigger)
	loc, err := time.LoadLocation(r.spec.Timezone)
	if err != nil {
		loc = time.UTC
	}
	job := FilesJob{Name: r.spec.Name, Paths: r.spec.Paths, Excludes: r.spec.Excludes, Subdir: r.spec.DestSubdir, Keep: int(r.spec.Keep), SkipRetention: true}
	if r.spec.Kind == "database" {
		job.DB = r.spec.Db
	}
	phase := func(name string, pct int) {
		pr := proto.Clone(base).(*pb.RunReport)
		pr.State, pr.Phase, pr.ProgressPct = pb.RunReport_PROGRESS, name, int32(pct)
		c.trySend(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: pr}})
	}
	phase("archiving", 0)
	var res Result
	if perr := c.policyCheck(r.spec); perr != nil {
		err = perr
	} else {
		res, err = c.exec(ctx, job, loc, started, Hooks{Logf: logf, Progress: func(p Result) {
			pr := proto.Clone(base).(*pb.RunReport)
			pr.State, pr.Phase, pr.BytesSource, pr.BytesOutput, pr.Files = pb.RunReport_PROGRESS, "archiving", p.BytesSource, p.BytesOutput, p.Files
			c.trySend(&pb.AgentMessage{Body: &pb.AgentMessage_RunReport{RunReport: pr}})
		}})
	}

	cloudStatus, cloudErr := "none", error(nil)
	var cinfo CloudInfo
	if err == nil && r.spec.CloudAccountId != 0 {
		logf("info", "enviando para a nuvem (%s)", r.spec.CloudFolder)
		last := -1
		cinfo, cloudErr = c.upload(ctx, r.spec, r.uid, res, Hooks{Logf: logf}, func(pct int) {
			if pct/5 != last/5 { // a cada 5%
				last = pct
				phase("uploading", pct)
			}
		})
		if cloudErr == nil {
			cloudStatus = "ok"
			logf("info", "nuvem: %s (verificado por %s)", cinfo.Path, cinfo.VerifiedBy)
			for _, l := range cinfo.Retention {
				logf("info", "nuvem: %s", l)
			}
		} else if ctx.Err() == nil {
			cloudStatus = "failed"
			logf("error", "envio para a nuvem falhou: %v", cloudErr)
		}
	}
	if err == nil && ctx.Err() == nil {
		c.localRetention(res, int(r.spec.Keep), cloudStatus == "ok", logf)
	}

	fin := proto.Clone(base).(*pb.RunReport)
	fin.State, fin.FinishedMs = pb.RunReport_FINISHED, ms(c.now())
	fin.BytesSource, fin.BytesOutput, fin.Files, fin.Sha256, fin.OutputName = res.BytesSource, res.BytesOutput, res.Files, res.SHA256, res.OutputName
	fin.CloudStatus, fin.CloudPath, fin.CloudHash, fin.CloudVerifiedBy = cloudStatus, cinfo.Path, cinfo.Hash, cinfo.VerifiedBy
	switch {
	case (err != nil || cloudErr != nil) && ctx.Err() != nil && parent.Err() == nil:
		fin.Status, fin.Error = "canceled", "cancelado pelo usuário"
		logf("warn", "backup cancelado")
	case (err != nil || cloudErr != nil) && parent.Err() != nil:
		return // o agente está encerrando: sem relatório; o próximo início recupera pelo horário (se dentro da janela)
	case err != nil:
		fin.Status, fin.Error = "failed", err.Error()
		logf("error", "falhou: %v", err)
	case cloudErr != nil:
		fin.Status, fin.Error = "failed", "o backup local foi concluído e conferido, mas o envio para a nuvem falhou: "+cloudErr.Error()
		logf("error", "o arquivo local foi preservado em %s", res.OutputPath)
	case res.Warnings > 0:
		fin.Status = "warning"
		logf("warn", "concluído com %d aviso(s)", res.Warnings)
	default:
		fin.Status = "ok"
		logf("info", "concluído com sucesso em %s", c.now().Sub(started).Round(time.Second))
	}
	stopFlush()
	<-flushDone
	lmu.Lock()
	fin.Logs = logs
	lmu.Unlock()
	c.deliver(fin)
}

// localRetention aplica a retenção local depois do fim do backup. Com keep=0 (só faz sentido com nuvem) o arquivo local
// é apagado depois que a nuvem o confirmou; se o envio falhou, o arquivo local fica.
func (c *Core) localRetention(res Result, keep int, cloudOK bool, logf func(level, f string, a ...any)) {
	if res.OutputPath == "" {
		return
	}
	if keep <= 0 {
		if cloudOK {
			if os.Remove(res.OutputPath) == nil {
				logf("info", "cópia local removida (a nuvem tem o arquivo verificado)")
			}
			return
		}
		keep = 1
	}
	if removed := ApplyRetention(res.DestDir, res.Prefix, keep); len(removed) > 0 {
		logf("info", "retenção local (manter %d): %d cópia(s) antiga(s) removida(s)", keep, len(removed))
	}
}

// ActiveRuns lista as execuções enfileiradas ou em andamento (enviada no Hello).
func (c *Core) ActiveRuns() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.active))
	for u := range c.active {
		out = append(out, u)
	}
	return out
}

// ---------------------------------------------------------------- nuvem

func (c *Core) waiter(id string) chan *pb.ServerMessage {
	ch := make(chan *pb.ServerMessage, 1)
	c.mu.Lock()
	c.waiters[id] = ch
	c.mu.Unlock()
	return ch
}

func (c *Core) dropWaiter(id string) {
	c.mu.Lock()
	delete(c.waiters, id)
	c.mu.Unlock()
}

func (c *Core) deliverWaiter(id string, m *pb.ServerMessage) {
	c.mu.Lock()
	ch := c.waiters[id]
	c.mu.Unlock()
	if ch != nil {
		select {
		case ch <- m:
		default:
		}
	}
}

func (c *Core) OnUploadSession(m *pb.UploadSession) {
	c.deliverWaiter(m.RequestId, &pb.ServerMessage{Body: &pb.ServerMessage_UploadSession{UploadSession: m}})
}

func (c *Core) OnUploadVerdict(m *pb.UploadVerdict) {
	c.deliverWaiter(m.RequestId, &pb.ServerMessage{Body: &pb.ServerMessage_UploadVerdict{UploadVerdict: m}})
}

// ask envia a mensagem (criada por mk com um request_id novo) e espera a resposta; reenvia se a conexão cair, por até maxWait.
func (c *Core) ask(ctx context.Context, maxWait time.Duration, mk func(id string) *pb.AgentMessage) (*pb.ServerMessage, error) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		id := auth.NewUUID()
		ch := c.waiter(id)
		if !c.trySend(mk(id)) {
			c.dropWaiter(id)
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		select {
		case m := <-ch:
			c.dropWaiter(id)
			return m, nil
		case <-time.After(2 * time.Minute):
			c.dropWaiter(id)
		case <-ctx.Done():
			c.dropWaiter(id)
			return nil, ctx.Err()
		}
	}
	return nil, errors.New("o Server não respondeu (sem conexão com o canal dos agentes)")
}

// uploadToCloud: pede a sessão ao Server, envia direto à nuvem, informa o resultado e recebe a conferência.
func (c *Core) uploadToCloud(ctx context.Context, spec *pb.JobSpec, runUID string, res Result, h Hooks, progress func(pct int)) (CloudInfo, error) {
	var info CloudInfo
	if res.OutputPath == "" {
		return info, errors.New("arquivo de backup não encontrado")
	}
	var up uploader.Result
	var sess *pb.UploadSession
	for attempt := 0; attempt < 2; attempt++ { // a segunda tentativa só acontece se a sessão expirar
		m, err := c.ask(ctx, 15*time.Minute, func(id string) *pb.AgentMessage {
			return &pb.AgentMessage{Body: &pb.AgentMessage_UploadRequest{UploadRequest: &pb.UploadRequest{RequestId: id, RunUid: runUID, JobId: spec.JobId, FileName: res.OutputName, Size: res.BytesOutput}}}
		})
		if err != nil {
			return info, err
		}
		sess = m.GetUploadSession()
		if sess == nil || !sess.Ok {
			msg := "o Server recusou o envio"
			if sess != nil && sess.Error != "" {
				msg = sess.Error
			}
			return info, errors.New(msg)
		}
		h.log("info", "sessão de upload aberta; enviando %s", HumanBytes(res.BytesOutput))
		opt := c.httpc.opt
		opt.Progress = func(sent, total int64) { progress(int(sent * 100 / total)) }
		up, err = uploader.Upload(ctx, uploader.Session{Protocol: sess.Protocol, URL: sess.Url, ChunkSize: sess.ChunkSize}, res.OutputPath, opt)
		if err == nil {
			break
		}
		if errors.Is(err, uploader.ErrSessionExpired) && attempt == 0 {
			h.log("warn", "a sessão de upload expirou; abrindo outra")
			continue
		}
		return info, err
	}
	h.log("info", "envio concluído; aguardando a conferência do Server")
	m, err := c.ask(ctx, 10*time.Minute, func(id string) *pb.AgentMessage {
		return &pb.AgentMessage{Body: &pb.AgentMessage_UploadDone{UploadDone: &pb.UploadDone{RequestId: id, RunUid: runUID, JobId: spec.JobId, FileName: res.OutputName,
			Size: up.Digest.Size, Md5: up.Digest.MD5, Sha1: up.Digest.SHA1, Quickxor: up.Digest.QuickXor, Sha256: up.Digest.SHA256, RemoteId: up.RemoteID, Ok: true}}}
	})
	if err != nil {
		return info, err
	}
	v := m.GetUploadVerdict()
	if v == nil || !v.Ok {
		msg := "o Server não confirmou o arquivo na nuvem"
		if v != nil && v.Message != "" {
			msg = v.Message
		}
		return info, errors.New(msg)
	}
	return CloudInfo{Path: v.RemotePath, Hash: v.RemoteHash, VerifiedBy: v.VerifiedBy, Retention: v.RetentionLog}, nil
}

// SetUploadOptions ajusta o cliente de upload (tentativas, espera entre tentativas); usado em testes.
func (c *Core) SetUploadOptions(o uploader.Options) { c.httpc.opt = o }

// PathAllowed diz se p é uma das pastas permitidas ou está dentro de uma delas (lista vazia = tudo permitido).
func PathAllowed(allowed []string, p string) bool {
	if len(allowed) == 0 {
		return true
	}
	p = path.Clean("/" + strings.TrimPrefix(p, "/"))
	for _, a := range allowed {
		a = path.Clean("/" + strings.TrimPrefix(strings.TrimSpace(a), "/"))
		if a == "/" || p == a || strings.HasPrefix(p, a+"/") {
			return true
		}
	}
	return false
}

// ContainerAllowed confere o nome do contêiner com os padrões permitidos (lista vazia = qualquer um).
func ContainerAllowed(patterns []string, name string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if ok, _ := path.Match(strings.TrimSpace(p), name); ok {
			return true
		}
	}
	return false
}

func (c *Core) policyCheck(spec *pb.JobSpec) error {
	if spec.Kind == "database" {
		if spec.Db != nil && spec.Db.Mode == "docker" && !ContainerAllowed(c.cfg.AllowedContainers, spec.Db.Container) {
			return fmt.Errorf("este agente não tem permissão para acessar o contêiner %q (CORUJA_ALLOWED_CONTAINERS)", spec.Db.Container)
		}
		return nil
	}
	for _, p := range spec.Paths {
		if !PathAllowed(c.cfg.AllowedPaths, p) {
			return fmt.Errorf("o caminho %q não é permitido por este agente (CORUJA_ALLOWED_PATHS)", p)
		}
	}
	return nil
}
