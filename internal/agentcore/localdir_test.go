package agentcore

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

func mk(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, p)
		if strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheckLocalDir(t *testing.T) {
	for _, bad := range []string{"", "relativo", "/", "//", "/proc", "/proc/1", "/sys/x", "/dev/shm", "/run/x", "/srv/../etc", "/a\nb", "/a\x00"} {
		if _, err := CheckLocalDir(bad); err == nil {
			t.Errorf("%q deveria ser recusado", bad)
		}
	}
	for in, want := range map[string]string{"/mnt/hd2/": "/mnt/hd2", "/home//ana/Cópias": "/home/ana/Cópias", "/devel": "/devel", "/runner/x": "/runner/x"} {
		if got, err := CheckLocalDir(in); err != nil || got != want {
			t.Errorf("%q => %q %v", in, got, err)
		}
	}
}

func TestEscolhaDaPastaLocalComEspaco(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "mnt/dados/", "mnt/arquivo.txt", "proc/", "sys/", "srv/.oculta/")
	if err := os.Symlink("/mnt", filepath.Join(root, "atalho")); err != nil {
		t.Fatal(err)
	}
	r, err := BrowseDisk(root, "/", nil)
	if err != nil || !slices.Equal(names(r), []string{"mnt/", "srv/"}) || r.Free <= 0 || r.Total < r.Free || r.Blocked == "" {
		t.Fatalf("raiz: só pastas utilizáveis, com espaço, e não selecionável: %v %v %+v", err, names(r), r)
	}
	r, err = BrowseDisk(root, "/mnt", nil)
	if err != nil || !slices.Equal(names(r), []string{"dados/"}) || r.Blocked != "" || !r.Exists {
		t.Fatalf("%v %+v", err, r)
	}
	// pasta que será criada: mostra o espaço do disco onde ficará
	r, err = BrowseDisk(root, "/mnt/nova/sub", nil)
	if err != nil || r.Exists || r.Free <= 0 || len(r.Entries) != 0 || r.Blocked != "" {
		t.Fatalf("%v %+v", err, r)
	}
	for _, p := range []string{"/atalho", "/atalho/dados", "/mnt/arquivo.txt", "/mnt/arquivo.txt/x", "/proc", "/sys/kernel"} {
		if _, err := BrowseDisk(root, p, nil); err == nil {
			t.Errorf("%s deveria ser recusado", p)
		}
	}
	// CORUJA_BACKUP_ROOTS limita onde se pode gravar
	r, err = BrowseDisk(root, "/", []string{"/mnt/dados"})
	if err != nil || !slices.Equal(names(r), []string{"mnt/"}) {
		t.Fatalf("%v %v", err, names(r))
	}
	if r, err = BrowseDisk(root, "/mnt", []string{"/mnt/dados"}); err != nil || r.Blocked == "" {
		t.Fatalf("o caminho até a pasta permitida não é selecionável: %v %+v", err, r)
	}
	if _, err = BrowseDisk(root, "/srv", []string{"/mnt/dados"}); err == nil {
		t.Fatal("fora do permitido")
	}

	c := &Core{cfg: Config{BackupDir: "/backups", HostWriteRoot: root, BackupRoots: []string{"/mnt"}}}
	if got, err := c.localDir("/mnt/dados"); err != nil || got != filepath.Join(root, "mnt/dados") || c.shownPath(got+"/a") != "/mnt/dados/a" {
		t.Fatalf("%q %v", got, err)
	}
	for _, p := range []string{"/srv/x", "/atalho/x", "/", "/proc/x"} {
		if _, err := c.localDir(p); err == nil {
			t.Errorf("localDir(%s) deveria falhar", p)
		}
	}
	if got, err := c.localDir("job-antigo"); err != nil || got != "/backups/job-antigo" {
		t.Fatalf("nome relativo continua na pasta padrão: %q %v", got, err)
	}
	for _, bad := range []string{"", filepath.Join(root, "nao-existe")} {
		c.cfg.HostWriteRoot = bad
		if _, err := c.localDir("/mnt/dados"); err == nil || !strings.Contains(err.Error(), "/:/host-rw") {
			t.Fatalf("sem a montagem gravável, a mensagem precisa ensinar o que fazer: %v", err)
		}
	}
}

// O job salva /home e grava dentro de /home: os outros dados dessa pasta continuam no backup, e só o que o agente gerou
// (deste job e de outro job que grava ali perto) fica de fora.
func TestPastaEscolhidaIgnoraSoOQueOAgenteGerou(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "home/ana/nota.txt", "home/planilha.ods", "home/casa-20250101-000000.tar.gz", "home/.casa-20250102-000000.tar.gz.tmp",
		"home/.tmp-0123456789ab/dump.sql", "home/outro-20250101-000000.tar.gz", "home/bancos/loja-20250101-000000.tar.gz", "home/bancos/leia-me.txt",
		"home/ana/casa-20250101-000000.tar.gz")
	st, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := New(Config{HostRoot: root, HostWriteRoot: root, BackupDir: t.TempDir()}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.jobs = map[int64]*pb.JobSpec{1: {JobId: 1, Name: "casa", DestSubdir: "/home"}, 2: {JobId: 2, Name: "loja", DestSubdir: "/home/bancos"}}
	want := []string{"home/", "home/ana/", "home/ana/casa-20250101-000000.tar.gz", "home/ana/nota.txt", "home/bancos/", "home/bancos/leia-me.txt",
		"home/outro-20250101-000000.tar.gz", "home/planilha.ods"}
	for i := range 2 {
		res, err := c.exec(context.Background(), FilesJob{Name: "casa", Paths: []string{"/home"}, Subdir: "/home", Keep: 1, SkipRetention: true}, time.UTC,
			time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC), discard())
		if err != nil {
			t.Fatal(err)
		}
		if res.DestDir != filepath.Join(root, "home") {
			t.Fatalf("gravou em %s", res.DestDir)
		}
		if got := tarNames(t, res.OutputPath); !slices.Equal(got, want) {
			t.Fatalf("execução %d:\n%v\nesperado:\n%v", i, got, want)
		}
	}
	// a retenção na pasta escolhida só apaga arquivos com o nome do job
	ApplyRetention(filepath.Join(root, "home"), "casa", 1)
	for _, f := range []string{"home/planilha.ods", "home/outro-20250101-000000.tar.gz", "home/ana/casa-20250101-000000.tar.gz"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("a retenção apagou o que não era do job: %s", f)
		}
	}
}

func TestSistemasVirtuaisFicamDeFora(t *testing.T) {
	info := `25 30 0:23 / /host/proc rw,nosuid - proc proc rw
26 30 0:24 / /host/sys rw - sysfs sysfs rw
27 30 0:5 / /host/dev rw - devtmpfs udev rw
30 1 8:1 / /host rw - ext4 /dev/sda1 rw
31 30 0:50 / /host/var/lib/docker/overlay2/abc/merged rw - overlay overlay rw
32 30 0:51 / /host/tmp rw - tmpfs tmpfs rw
33 1 0:52 / / rw - overlay overlay rw
`
	got := virtualMounts(strings.NewReader(info))
	want := map[string]string{"/host/proc": "proc", "/host/sys": "sysfs", "/host/dev": "devtmpfs", "/host/var/lib/docker/overlay2/abc/merged": "overlay"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q", k, got[k])
		}
	}
}
