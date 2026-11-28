package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/netdest"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

// Backup em pasta em storages reais (veja scripts/test-netdest.sh): full, incremental (link físico no NFS e no SFTP,
// cópia completa no SMB e no FTP), restauração lendo do storage, item adulterado recusado, retenção apagando a pasta
// antiga e cancelamento sem sobra.
func TestBackupEmPastaNosStorages(t *testing.T) {
	cfgs := map[string]netdest.Config{}
	if h := os.Getenv("CORUJA_TEST_SFTP"); h != "" {
		cfgs["sftp"] = netdest.Config{Type: netdest.TypeSFTP, Host: h, User: "coruja", Password: "s3nh@-forte", BasePath: "upload/base"}
	}
	if h := os.Getenv("CORUJA_TEST_FTP"); h != "" {
		cfgs["ftp"] = netdest.Config{Type: netdest.TypeFTP, Host: h, User: "coruja", Password: "s3nh@-forte", TLS: netdest.TLSNone}
	}
	if h := os.Getenv("CORUJA_TEST_SMB"); h != "" {
		cfgs["smb"] = netdest.Config{Type: netdest.TypeSMB, Host: h, User: "coruja", Password: "s3nh@-forte", Share: "backups", BasePath: "base"}
	}
	if spec := os.Getenv("CORUJA_TEST_NFS"); spec != "" {
		parts := strings.SplitN(spec, ":", 3)
		port, _ := strconv.Atoi(parts[1])
		c := netdest.Config{Type: netdest.TypeNFS, Host: parts[0], Port: port, Export: parts[2], BasePath: os.Getenv("CORUJA_TEST_NFS_BASE")}
		if v := os.Getenv("CORUJA_TEST_NFS_UID"); v != "" {
			n, _ := strconv.Atoi(v)
			c.UID, c.GID = &n, &n
		}
		cfgs["nfs"] = c
	}
	if len(cfgs) == 0 {
		t.Skip("defina CORUJA_TEST_SFTP/FTP/SMB/NFS (scripts/test-netdest.sh)")
	}
	for typ, cfg := range cfgs {
		t.Run(typ, func(t *testing.T) { dirOnStorage(t, typ, cfg) })
	}
}

func dirOnStorage(t *testing.T, typ string, cfg netdest.Config) {
	ctx, cancelAll := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancelAll()
	if cfg.Type == netdest.TypeSFTP {
		tr, err := netdest.Test(ctx, cfg, netdest.Env{})
		if err != nil {
			t.Fatal(err)
		}
		cfg.HostKey = tr.HostKey
	}
	root, bk := t.TempDir(), t.TempDir()
	st, _ := NewState(t.TempDir())
	c := New(Config{HostRoot: root, HostWriteRoot: root, BackupDir: bk}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	logs := logSink(c)
	src := filepath.Join(root, "srv")
	write(t, filepath.Join(src, "a.txt"), "a v1")
	write(t, filepath.Join(src, "grande.bin"), noise(2<<20, 3))
	write(t, filepath.Join(src, "sub", "pasta", "c.txt"), "c v1")
	os.Symlink("a.txt", filepath.Join(src, "atalho"))
	folder := "Coruja Backup/pasta-" + typ + "-" + time.Now().Format("150405.000")
	b, _ := json.Marshal(cfg)
	c.jobs[1] = &pb.JobSpec{JobId: 1, Name: "Pasta", Kind: "files", Format: FormatDir, Dest: &pb.DestSpec{Type: cfg.Type, Config: b, Folder: folder, Keep: 1}}
	t0 := time.Date(2026, 11, 28, 3, 0, 0, 0, time.UTC)
	job := FilesJob{Name: "Pasta", Paths: []string{"/srv"}}
	run := func(ctx context.Context, day int, base *Snapshot) (Result, error) {
		t.Helper()
		tg, err := netdest.OpenTarget(ctx, cfg, netdest.Env{}, folder)
		if err != nil {
			return Result{}, err
		}
		defer tg.Close()
		j := job
		j.inc = &incPlan{base: base}
		var lines []string
		h := Hooks{Logf: func(_, f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }}
		res, err := RunDir(ctx, j, dirTarget{t: tg, keep: 1}, root, bk, time.UTC, t0.Add(time.Duration(day)*24*time.Hour), h)
		for _, l := range lines {
			if strings.Contains(l, "link físico falhou") {
				t.Logf("%s: %s", typ, l)
			}
		}
		if err == nil {
			st.SaveCatalog(1, res.OutputName, res.Snapshot)
		}
		return res, err
	}
	r1, err := run(ctx, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Stored == nil || r1.Stored.Path != folder+"/"+r1.OutputName || r1.Stored.VerifiedBy != "sha256" {
		t.Fatalf("resultado: %+v", r1.Stored)
	}
	time.Sleep(10 * time.Millisecond)
	write(t, filepath.Join(src, "a.txt"), "a v2, maior")
	r2, err := run(ctx, 1, r1.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	// NFS: o servidor de teste em espaço de usuário (go-nfs) não cria links físicos; o agente cai na cópia completa
	links := typ == "sftp" || (typ == "nfs" && os.Getenv("CORUJA_TEST_NFS_LINKS") == "1")
	if links && (r2.Kind != ModeIncremental || r2.BytesSource != int64(len("a v2, maior"))) {
		t.Fatalf("%s tem link físico: só o alterado deveria ser copiado (%s, %d bytes)", typ, r2.Kind, r2.BytesSource)
	}
	if !links && (r2.Kind != ModeFull || r2.BytesSource < 2<<20) {
		t.Fatalf("%s não tem link físico: a pasta deveria ser uma cópia completa (%s, %d bytes)", typ, r2.Kind, r2.BytesSource)
	}
	// retenção (manter 1): a pasta anterior saiu, e a nova continua inteira
	if len(r2.Stored.Retention) != 1 || !strings.Contains(r2.Stored.Retention[0], r1.OutputName) {
		t.Fatalf("retenção: %v", r2.Stored.Retention)
	}
	act := func(target string, paths ...string) *pb.AgentAction {
		return &pb.AgentAction{Kind: "restore_files", JobId: 1, FileName: r2.OutputName, Paths: paths, Target: target, Sha256: r2.SHA256}
	}
	a := act("tudo", "/srv")
	if err := c.restoreFiles(ctx, a, &actionLog{c: c, act: a}); err != nil {
		t.Fatal(err)
	}
	if d := diff(tree(t, root), tree(t, filepath.Join(bk, "restores", "tudo"))); d != "" {
		t.Fatalf("restauração do storage:\n%s", d)
	}
	if l := logs(); !strings.Contains(l, "(storage)") {
		t.Fatalf("deveria ler do storage:\n%s", l)
	}
	// sem o catálogo no agente: o da pasta, no storage
	catCache = &catalogCache{items: map[string]*Snapshot{}}
	st2, _ := NewState(t.TempDir())
	c.st = st2
	a = act("sem-catalogo", "/srv/sub")
	if err := c.restoreFiles(ctx, a, &actionLog{c: c, act: a}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(bk, "restores", "sem-catalogo", "srv", "sub", "pasta", "c.txt")); string(b) != "c v1" {
		t.Fatal("restauração com o catálogo da pasta no storage")
	}
	// adulterado no storage: recusado
	conn, _, err := netdest.Dial(ctx, cfg, netdest.Env{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	dir := path.Join(folder, r2.OutputName, "srv")
	if err := conn.Write(ctx, dir, "a.txt", strings.NewReader("a v2, MAIOR")); err != nil {
		t.Fatal(err)
	}
	a = act("adulterado", "/srv/a.txt")
	if err := c.restoreFiles(ctx, a, &actionLog{c: c, act: a}); err == nil || !strings.Contains(err.Error(), "não confere") {
		t.Fatalf("adulterado deveria ser recusado: %v", err)
	}
	// cancelado: a pasta provisória não fica
	cctx, cancel := context.WithCancel(ctx)
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	if _, err := run(cctx, 2, r2.Snapshot); err == nil {
		t.Log("o backup terminou antes do cancelamento (rápido demais): só confere que não sobrou provisória")
	}
	dirs, err := conn.Dirs(folder)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if strings.HasSuffix(d, netdest.PartSuffix) {
			t.Fatalf("sobrou a pasta provisória %s", d)
		}
	}
	// limpeza
	if err := conn.RemoveAll(folder); err != nil {
		t.Fatal(err)
	}
	if ds, _ := conn.Dirs(path.Dir(folder)); len(ds) > 0 {
		for _, d := range ds {
			if d == path.Base(folder) {
				t.Fatal("RemoveAll deveria apagar a pasta do job")
			}
		}
	}
}
