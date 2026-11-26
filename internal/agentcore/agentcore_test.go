package agentcore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

func TestExcluder(t *testing.T) {
	e := NewExcluder([]string{"*/data/cache/*", "*.tmp", "/var/log/*", ""})
	yes := []string{"/srv/app/data/cache/x", "/a/b.tmp", "/b.tmp", "/var/log/syslog"}
	no := []string{"/srv/app/data/logs/x", "/a/b.tmpx", "/var/logx", "/srv/app/index.html"}
	for _, p := range yes {
		if !e.Match(p) {
			t.Errorf("deveria excluir %s", p)
		}
	}
	for _, p := range no {
		if e.Match(p) {
			t.Errorf("não deveria excluir %s", p)
		}
	}
}

func TestSafeSubdir(t *testing.T) {
	for _, bad := range []string{"", "/etc", "..", "../x", "a/../../x"} {
		if _, err := SafeSubdir("/backups", bad); err == nil {
			t.Errorf("aceitou %q", bad)
		}
	}
	if p, err := SafeSubdir("/backups", "umami/server2"); err != nil || p != "/backups/umami/server2" {
		t.Fatalf("%v %v", p, err)
	}
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func listTar(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		if h.Typeflag == tar.TypeSymlink {
			out[h.Name] = "-> " + h.Linkname
		} else {
			out[h.Name] = string(b)
		}
	}
}

func discard() Hooks { return Hooks{} }

func TestRunFilesConteudoExcludesERetencao(t *testing.T) {
	root, bk := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "srv/app/index.html"), "ola")
	write(t, filepath.Join(root, "srv/app/data/cache/c.bin"), "cache")
	write(t, filepath.Join(root, "srv/app/data/logs/l.txt"), "log")
	write(t, filepath.Join(root, "srv/app/notas.tmp"), "tmp")
	write(t, filepath.Join(root, "etc/conf"), "conf")
	if err := os.Symlink("index.html", filepath.Join(root, "srv/app/atalho")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "srv/app/fila"), 0o644); err != nil {
		t.Fatal(err)
	}
	var logs []string
	h := Hooks{Logf: func(l, f string, a ...any) { logs = append(logs, l+": "+f) }}
	job := FilesJob{Name: "Meu Backup", Paths: []string{"/srv/app", "/etc", "/nao/existe"}, Excludes: []string{"*/data/cache/*", "*/data/logs/*", "*.tmp"}, Subdir: "grupo/job", Keep: 2}
	base := time.Date(2026, 11, 26, 0, 10, 0, 0, time.UTC)
	var names []string
	for i := 0; i < 4; i++ {
		res, err := RunFiles(context.Background(), job, root, bk, time.UTC, base.Add(time.Duration(i)*time.Minute), h)
		if err != nil {
			t.Fatal(err)
		}
		if i == 3 {
			names = append(names, res.OutputName)
			if res.Warnings != 2 || res.Files == 0 || res.SHA256 == "" || res.BytesOutput == 0 {
				t.Fatalf("resultado inesperado: %+v (origem inexistente e fifo deveriam gerar 2 avisos)", res)
			}
		}
	}
	dir := filepath.Join(bk, "grupo/job")
	ents, _ := os.ReadDir(dir)
	var got []string
	for _, e := range ents {
		got = append(got, e.Name())
	}
	sort.Strings(got)
	if len(got) != 2 || got[1] != "meu-backup-20261126-001300.tar.gz" {
		t.Fatalf("retenção deveria manter só as 2 mais novas: %v", got)
	}
	content := listTar(t, filepath.Join(dir, got[1]))
	if content["srv/app/index.html"] != "ola" || content["etc/conf"] != "conf" || content["srv/app/atalho"] != "-> index.html" {
		t.Fatalf("conteúdo inesperado: %v", content)
	}
	for k := range content {
		// a pasta "cache/" em si pode aparecer (vazia), como no tar --exclude; o conteúdo, não
		if strings.Contains(k, "c.bin") || strings.Contains(k, "l.txt") || strings.HasSuffix(k, ".tmp") || strings.HasSuffix(k, "fila") {
			t.Fatalf("item excluído apareceu no backup: %s", k)
		}
	}
	if ents, _ := os.ReadDir(dir); func() bool {
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".tmp") {
				return true
			}
		}
		return false
	}() {
		t.Fatal("sobrou arquivo temporário")
	}
}

func TestRunFilesNadaParaSalvarEDestinoInvalido(t *testing.T) {
	root, bk := t.TempDir(), t.TempDir()
	_, err := RunFiles(context.Background(), FilesJob{Name: "x", Paths: []string{"/nada"}, Subdir: "a", Keep: 1}, root, bk, time.UTC, time.Now(), discard())
	if err == nil || !strings.Contains(err.Error(), "nada foi salvo") {
		t.Fatalf("esperava erro de nada salvo: %v", err)
	}
	if ents, _ := os.ReadDir(filepath.Join(bk, "a")); len(ents) != 0 {
		t.Fatalf("não deveria sobrar arquivo: %v", ents)
	}
	if _, err := RunFiles(context.Background(), FilesJob{Name: "x", Paths: []string{"/"}, Subdir: "../fora", Keep: 1}, root, bk, time.UTC, time.Now(), discard()); err == nil {
		t.Fatal("destino fora da área de backups deveria ser recusado")
	}
}

func TestRunFilesCancelamentoNaoDeixaArquivo(t *testing.T) {
	root, bk := t.TempDir(), t.TempDir()
	for i := 0; i < 50; i++ {
		write(t, filepath.Join(root, "d", string(rune('a'+i%26)), "f"+string(rune('0'+i%10))+".txt"), strings.Repeat("x", 1000))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunFiles(ctx, FilesJob{Name: "c", Paths: []string{"/d"}, Subdir: "c", Keep: 1}, root, bk, time.UTC, time.Now(), discard())
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("esperava ErrCanceled: %v", err)
	}
	if ents, _ := os.ReadDir(filepath.Join(bk, "c")); len(ents) != 0 {
		t.Fatalf("cancelamento não pode deixar arquivo: %v", ents)
	}
}

func TestVerifyArchiveDetectaCorrupcao(t *testing.T) {
	root, bk := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "a/f.txt"), strings.Repeat("dados", 2000))
	res, err := RunFiles(context.Background(), FilesJob{Name: "v", Paths: []string{"/a"}, Subdir: "v", Keep: 1}, root, bk, time.UTC, time.Now(), discard())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(bk, "v", res.OutputName)
	if err := VerifyArchive(context.Background(), p, res.SHA256, 0); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	b[len(b)/2] ^= 0xff
	os.WriteFile(p, b, 0o640)
	if err := VerifyArchive(context.Background(), p, res.SHA256, 0); err == nil {
		t.Fatal("arquivo corrompido deveria falhar na verificação")
	}
}

func TestRetencaoSoMexeEmArquivosDoJob(t *testing.T) {
	d := t.TempDir()
	for _, n := range []string{"job-20260101-000000.tar.gz", "job-20260102-000000.tar.gz", "job-20260103-000000.tar.gz", "outro-20260101-000000.tar.gz", "job-notas.tar.gz", "leia-me.txt"} {
		os.WriteFile(filepath.Join(d, n), []byte("x"), 0o644)
	}
	rem := ApplyRetention(d, "job", 1)
	if len(rem) != 2 {
		t.Fatalf("removidos: %v", rem)
	}
	for _, keep := range []string{"job-20260103-000000.tar.gz", "outro-20260101-000000.tar.gz", "job-notas.tar.gz", "leia-me.txt"} {
		if _, err := os.Stat(filepath.Join(d, keep)); err != nil {
			t.Fatalf("%s não deveria ter sido apagado", keep)
		}
	}
}

// ---------------------------------------------------------------- estado e agendador

func TestSpoolEJobsPersistem(t *testing.T) {
	st, _ := NewState(t.TempDir())
	st.SpoolAdd(&pb.RunReport{RunUid: "b", JobId: 1, FinishedMs: 20, Status: "ok"})
	st.SpoolAdd(&pb.RunReport{RunUid: "a", JobId: 1, FinishedMs: 10, Status: "failed", Logs: []*pb.LogLine{{Seq: 1, Text: "x"}}})
	l := st.SpoolList()
	if len(l) != 2 || l[0].RunUid != "a" || l[0].Logs[0].Text != "x" {
		t.Fatalf("%v", l)
	}
	st.SpoolRemove("a")
	if len(st.SpoolList()) != 1 {
		t.Fatal("remoção falhou")
	}
	st.SaveJobs([]*pb.JobSpec{{JobId: 7, Name: "n", Cron: "0 1 * * *", Paths: []string{"/etc"}}})
	if j := st.LoadJobs(); len(j) != 1 || j[0].JobId != 7 || j[0].Paths[0] != "/etc" {
		t.Fatalf("%v", j)
	}
}

type rig struct {
	core *Core
	now  time.Time
	mu   sync.Mutex
	sent []*pb.RunReport
	runs []string
	exec chan struct{}
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, _ := NewState(t.TempDir())
	r := &rig{now: time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC)}
	r.core = New(Config{HostRoot: "/", BackupDir: t.TempDir()}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.core.now = func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }
	r.core.exec = func(ctx context.Context, job FilesJob, loc *time.Location, now time.Time, h Hooks) (Result, error) {
		r.mu.Lock()
		r.runs = append(r.runs, job.Name)
		r.mu.Unlock()
		if r.exec != nil {
			select {
			case <-r.exec:
			case <-ctx.Done():
				return Result{}, ErrCanceled
			}
		}
		h.Logf("info", "rodando %s", job.Name)
		if job.Name == "falha" {
			return Result{}, errors.New("disco cheio")
		}
		if job.Name == "aviso" {
			return Result{OutputName: "x.tar.gz", Warnings: 1}, nil
		}
		return Result{OutputName: "x.tar.gz", Files: 3, BytesOutput: 10, SHA256: "abc"}, nil
	}
	r.core.SetSender(func(m *pb.AgentMessage) bool {
		if rr := m.GetRunReport(); rr != nil {
			r.mu.Lock()
			r.sent = append(r.sent, rr)
			r.mu.Unlock()
		}
		return true
	})
	return r
}

func (r *rig) advance(d time.Duration) { r.mu.Lock(); r.now = r.now.Add(d); r.mu.Unlock() }

func (r *rig) finished(t *testing.T, n int) []*pb.RunReport {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		var f []*pb.RunReport
		for _, x := range r.sent {
			if x.State == pb.RunReport_FINISHED {
				f = append(f, x)
			}
		}
		r.mu.Unlock()
		if len(f) >= n {
			return f
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("esperava %d relatórios finais", n)
	return nil
}

func spec(id int64, name, cron string) *pb.JobSpec {
	return &pb.JobSpec{JobId: id, Name: name, Kind: "files", Enabled: true, Cron: cron, Timezone: "UTC", Paths: []string{"/x"}, DestSubdir: "d", Keep: 3, Version: 1}
}

func TestAgendaDisparaNoHorarioEReportaOK(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.core.Run(ctx)
	r.core.SyncJobs([]*pb.JobSpec{spec(1, "diario", "10 3 * * *")}) // 03:10 UTC
	r.advance(5 * time.Minute)
	r.core.Tick()
	time.Sleep(100 * time.Millisecond)
	if len(r.runs) != 0 {
		t.Fatal("ainda não era a hora")
	}
	r.advance(6 * time.Minute) // 03:11
	r.core.Tick()
	f := r.finished(t, 1)
	if f[0].Status != "ok" || f[0].Trigger != "schedule" || f[0].ScheduledForMs != time.Date(2026, 11, 26, 3, 10, 0, 0, time.UTC).UnixMilli() || f[0].Sha256 != "abc" {
		t.Fatalf("%+v", f[0])
	}
	if len(f[0].Logs) < 2 || f[0].Logs[0].Seq != 1 {
		t.Fatalf("o log completo deve ir no relatório final: %v", f[0].Logs)
	}
	// ticks seguintes não repetem o mesmo horário
	r.advance(time.Minute)
	r.core.Tick()
	time.Sleep(100 * time.Millisecond)
	if len(r.runs) != 1 {
		t.Fatalf("o mesmo horário não pode rodar duas vezes: %v", r.runs)
	}
	// o resultado fica na fila local até o Server confirmar
	if len(r.core.st.SpoolList()) != 1 {
		t.Fatal("o resultado deveria estar na fila local")
	}
	r.core.Ack(f[0].RunUid)
	if len(r.core.st.SpoolList()) != 0 {
		t.Fatal("ack deveria limpar a fila")
	}
}

func TestStatusFalhaEAviso(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.core.Run(ctx)
	r.core.SyncJobs([]*pb.JobSpec{spec(1, "falha", "0 0 1 1 *"), spec(2, "aviso", "0 0 1 1 *")})
	r.core.Enqueue(1, "u1", "manual", time.Time{})
	r.core.Enqueue(2, "u2", "manual", time.Time{})
	f := r.finished(t, 2)
	by := map[string]*pb.RunReport{}
	for _, x := range f {
		by[x.RunUid] = x
	}
	if by["u1"].Status != "failed" || !strings.Contains(by["u1"].Error, "disco cheio") || by["u2"].Status != "warning" || by["u1"].Trigger != "manual" {
		t.Fatalf("%+v / %+v", by["u1"], by["u2"])
	}
}

func TestRecuperaHorarioPerdidoDentroDaJanela(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.core.Run(ctx)
	r.core.SyncJobs([]*pb.JobSpec{spec(1, "diario", "10 0 * * *")})
	// agente parado: passa a meia-noite e só volta às 03:30 (dentro da janela de 6h)
	r.advance(0)
	r.core.mu.Lock()
	r.core.lastFire[1] = time.Date(2026, 11, 25, 0, 10, 0, 0, time.UTC).Unix()
	r.core.mu.Unlock()
	r.core.Tick()
	f := r.finished(t, 1)
	if f[0].ScheduledForMs != time.Date(2026, 11, 26, 0, 10, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("deveria recuperar o horário das 00:10: %+v", f[0])
	}
}

func TestHorarioMuitoAntigoNaoERecuperado(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.core.Run(ctx)
	r.core.SyncJobs([]*pb.JobSpec{spec(1, "diario", "10 0 * * *")})
	r.advance(8 * time.Hour) // 11:00 — a última 00:10 foi há mais de 6h
	r.core.mu.Lock()
	r.core.lastFire[1] = time.Date(2026, 11, 25, 0, 10, 0, 0, time.UTC).Unix()
	r.core.mu.Unlock()
	r.core.Tick()
	time.Sleep(150 * time.Millisecond)
	if len(r.runs) != 0 {
		t.Fatalf("não deveria rodar um backup de 11 horas atrás: %v", r.runs)
	}
}

func TestJobNovoNaoCobraHorariosAnteriores(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.core.Run(ctx)
	r.core.SyncJobs([]*pb.JobSpec{spec(1, "novo", "0 * * * *")}) // criado às 03:00 exatos
	r.core.Tick()
	time.Sleep(150 * time.Millisecond)
	if len(r.runs) != 0 {
		t.Fatalf("job recém-criado não deve disparar o horário de agora/passado: %v", r.runs)
	}
}

func TestMesmoJobNaoRodaEmParaleloECancelamento(t *testing.T) {
	r := newRig(t)
	r.exec = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.core.Run(ctx)
	r.core.SyncJobs([]*pb.JobSpec{spec(1, "longo", "0 0 1 1 *")})
	r.core.Enqueue(1, "u1", "manual", time.Time{})
	time.Sleep(100 * time.Millisecond)
	r.core.Enqueue(1, "u2", "manual", time.Time{}) // enquanto u1 roda
	f := r.finished(t, 1)
	if f[0].RunUid != "u2" || f[0].Status != "failed" || !strings.Contains(f[0].Error, "ainda não terminou") {
		t.Fatalf("a segunda execução deveria ser pulada: %+v", f[0])
	}
	r.core.Cancel("u1")
	f = r.finished(t, 2)
	var u1 *pb.RunReport
	for _, x := range f {
		if x.RunUid == "u1" {
			u1 = x
		}
	}
	if u1 == nil || u1.Status != "canceled" {
		t.Fatalf("u1 deveria ficar cancelada: %+v", u1)
	}
}

func TestOfflineEnfileiraEReenvia(t *testing.T) {
	r := newRig(t)
	r.core.SetSender(nil) // Server fora do ar
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.core.Run(ctx)
	r.core.SyncJobs([]*pb.JobSpec{spec(1, "x", "0 0 1 1 *")})
	r.core.Enqueue(1, "u1", "manual", time.Time{})
	deadline := time.Now().Add(3 * time.Second)
	for len(r.core.st.SpoolList()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(r.core.st.SpoolList()) != 1 || len(r.sent) != 0 {
		t.Fatal("offline: o resultado deve ficar só na fila local")
	}
	r.core.SetSender(func(m *pb.AgentMessage) bool {
		r.mu.Lock()
		r.sent = append(r.sent, m.GetRunReport())
		r.mu.Unlock()
		return true
	})
	r.core.Replay()
	if f := r.finished(t, 1); f[0].RunUid != "u1" {
		t.Fatalf("%+v", f)
	}
}

// ---------------------------------------------------------------- Fase 5

func TestLogsSaemAoVivoDuranteAExecucao(t *testing.T) {
	r := newRig(t)
	r.exec = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.core.Run(ctx)
	r.core.SyncJobs([]*pb.JobSpec{spec(1, "longo", "0 0 1 1 *")})
	r.core.Enqueue(1, "u1", "manual", time.Time{})
	deadline := time.Now().Add(6 * time.Second)
	var live *pb.RunReport
	for time.Now().Before(deadline) && live == nil {
		r.mu.Lock()
		for _, x := range r.sent {
			if x.State == pb.RunReport_PROGRESS && len(x.Logs) > 0 {
				live = x
			}
		}
		r.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
	if live == nil || !strings.Contains(live.Logs[0].Text, "iniciando o job") {
		t.Fatalf("as linhas do log deveriam ser enviadas enquanto o backup ainda roda: %+v", live)
	}
	close(r.exec)
	if f := r.finished(t, 1); len(f[0].Logs) < 2 {
		t.Fatalf("o relatório final ainda leva o log completo: %v", f[0].Logs)
	}
}

type tarEntry struct {
	name, link, body string
	typ              byte
}

func makeTarGz(t *testing.T, ents []tarEntry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.tar.gz")
	f, _ := os.Create(p)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range ents {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o644, Size: int64(len(e.body)), Linkname: e.link, ModTime: time.Unix(1_700_000_000, 0)}
		if e.typ == tar.TypeDir {
			h.Mode = 0o755
		}
		tw.WriteHeader(h)
		tw.Write([]byte(e.body))
	}
	tw.Close()
	gz.Close()
	f.Close()
	return p
}

func TestExtracaoSegura(t *testing.T) {
	base := t.TempDir()
	dest := filepath.Join(base, "restores", "r1")
	outside := filepath.Join(base, "fora")
	os.MkdirAll(outside, 0o755)
	arc := makeTarGz(t, []tarEntry{
		{name: "etc/", typ: tar.TypeDir},
		{name: "etc/app.conf", body: "conf", typ: tar.TypeReg},
		{name: "../../fuga.txt", body: "mal", typ: tar.TypeReg}, // sai do destino
		{name: "/abs/nome.txt", body: "abs", typ: tar.TypeReg},  // absoluto => vira relativo
		{name: "ln", link: outside, typ: tar.TypeSymlink},       // link para fora
		{name: "ln/escapou.txt", body: "NAO", typ: tar.TypeReg}, // tentativa de escrever através do link
		{name: "ok/dentro.txt", body: "ok", typ: tar.TypeReg},
	})
	var warns int
	n, size, err := ExtractArchive(context.Background(), arc, dest, func(l, f string, a ...any) {
		if l == "warn" {
			warns++
		}
	})
	if err != nil || n == 0 || size == 0 {
		t.Fatalf("%d %d %v", n, size, err)
	}
	for _, want := range map[string]string{"etc/app.conf": "conf", "abs/nome.txt": "abs", "ok/dentro.txt": "ok", "fuga.txt": "mal"} {
		_ = want
	}
	read := func(p string) string { b, _ := os.ReadFile(filepath.Join(dest, p)); return string(b) }
	if read("etc/app.conf") != "conf" || read("abs/nome.txt") != "abs" || read("ok/dentro.txt") != "ok" {
		t.Fatal("itens legítimos deveriam ser extraídos")
	}
	if _, err := os.Stat(filepath.Join(base, "fuga.txt")); err == nil {
		t.Fatal("../ escapou do destino")
	}
	if _, err := os.Stat(filepath.Join(outside, "escapou.txt")); err == nil {
		t.Fatal("escreveu através de link simbólico para fora do destino")
	}
	if warns == 0 {
		t.Fatal("tentativas de escape deveriam gerar avisos")
	}
}

func TestAcaoReenvioExigeArquivoELocalValido(t *testing.T) {
	r := newRig(t)
	for _, name := range []string{"../../etc/passwd", "x.tar.gz", "job-20260101-000000.zip"} {
		if _, _, err := r.core.backupPath(&pb.AgentAction{FileName: name, Subdir: "d"}); err == nil {
			t.Errorf("aceitou %q", name)
		}
	}
	if _, _, err := r.core.backupPath(&pb.AgentAction{FileName: "job-20260101-000000.tar.gz", Subdir: "../fora"}); err == nil {
		t.Error("subpasta fora da área de backups aceita")
	}
	if _, _, err := r.core.backupPath(&pb.AgentAction{FileName: "job-20260101-000000.tar.gz", Subdir: "d"}); err == nil || !strings.Contains(err.Error(), "não existe mais") {
		t.Errorf("arquivo ausente: %v", err)
	}
}
