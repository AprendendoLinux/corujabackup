package agentcore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/netdest"
)

// storage "pasta local" num diretório temporário, como o agente o enxerga
func directTarget(t *testing.T, ctx context.Context, verify string) (*netdest.Target, string) {
	t.Helper()
	host := t.TempDir()
	env := netdest.Env{HostDir: func(abs string) (string, error) { return filepath.Join(host, abs), nil }}
	tg, err := netdest.OpenTarget(ctx, netdest.Config{Type: netdest.TypeLocal, Path: "/storage", Verify: verify}, env, "job")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tg.Close() })
	return tg, filepath.Join(host, "storage", "job")
}

func TestModoDiretoArquivos(t *testing.T) {
	root, bk := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "srv/a.txt"), strings.Repeat("abc", 50000))
	write(t, filepath.Join(root, "srv/b/c.txt"), "c")
	ctx := context.Background()
	base := time.Date(2026, 11, 27, 3, 0, 0, 0, time.UTC)
	// três execuções no mesmo storage: retenção de 2 cópias
	host := t.TempDir()
	env := netdest.Env{HostDir: func(abs string) (string, error) { return filepath.Join(host, abs), nil }}
	var names []string
	for i := 0; i < 3; i++ {
		tg, err := netdest.OpenTarget(ctx, netdest.Config{Type: netdest.TypeLocal, Path: "/storage"}, env, "job")
		if err != nil {
			t.Fatal(err)
		}
		out := &directOut{t: tg, job: netdest.Job{Folder: "job", Keep: 2, Now: time.Now()}}
		res, err := RunFiles(ctx, FilesJob{Name: "Direto", Paths: []string{"/srv"}, Subdir: "x", Keep: 1, out: out}, root, bk, time.UTC, base.Add(time.Duration(i)*time.Minute), discard())
		tg.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.Stored == nil || res.Stored.VerifiedBy != "sha256" || res.Stored.Hash != res.SHA256 || res.OutputPath != "" {
			t.Fatalf("resultado: %+v %+v", res, res.Stored)
		}
		if i == 2 && len(res.Stored.Retention) != 1 {
			t.Fatalf("a retenção do storage deveria remover 1: %v", res.Stored.Retention)
		}
		names = append(names, res.OutputName)
	}
	ents, _ := os.ReadDir(filepath.Join(host, "storage", "job"))
	if len(ents) != 2 || ents[0].Name() != names[1] || ents[1].Name() != names[2] {
		t.Fatalf("no storage deveriam ficar as 2 últimas, sem temporários: %v", ents)
	}
	if err := VerifyArchive(ctx, filepath.Join(host, "storage", "job", names[2]), "", 0); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatal("o arquivo no storage deveria ser um tar.gz válido (só o SHA vazio difere):", err)
	}
	if ents, _ := os.ReadDir(bk); len(ents) != 0 {
		t.Fatalf("o modo direto não pode gravar na pasta de backups do agente: %v", ents)
	}
}

func TestModoDiretoCancelamentoEConferenciaRapida(t *testing.T) {
	root, bk := t.TempDir(), t.TempDir()
	for i := 0; i < 40; i++ {
		write(t, filepath.Join(root, "d", string(rune('a'+i%26)), "f"+string(rune('0'+i%10))+".txt"), strings.Repeat("x", 4000))
	}
	ctx, cancel := context.WithCancel(context.Background())
	tg, dir := directTarget(t, context.Background(), "")
	cancel()
	_, err := RunFiles(ctx, FilesJob{Name: "c", Paths: []string{"/d"}, out: &directOut{t: tg, job: netdest.Job{Folder: "job", Keep: 1, Now: time.Now()}}}, root, bk, time.UTC, time.Now(), discard())
	if err == nil {
		t.Fatal("cancelado deveria falhar")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("o cancelamento não pode deixar nada no storage: %v", ents)
	}

	tg2, dir2 := directTarget(t, context.Background(), netdest.VerifySize)
	res, err := RunFiles(context.Background(), FilesJob{Name: "r", Paths: []string{"/d"}, out: &directOut{t: tg2, job: netdest.Job{Folder: "job", Keep: 1, Now: time.Now()}}}, root, bk, time.UTC, time.Now(), discard())
	if err != nil || res.Stored == nil || res.Stored.VerifiedBy != netdest.VerifySize {
		t.Fatal(res.Stored, err)
	}
	if _, err := os.Stat(filepath.Join(dir2, res.OutputName)); err != nil {
		t.Fatal(err)
	}
}

// Falha do storage no meio da gravação: o produtor recebe o erro e nada fica para trás.
func TestModoDiretoFalhaNoStorage(t *testing.T) {
	root, bk := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "d/f"), strings.Repeat("y", 300000))
	tg, dir := directTarget(t, context.Background(), "")
	// remove a pasta do job depois de aberta: a gravação do temporário falha
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("agora é um arquivo"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := RunFiles(context.Background(), FilesJob{Name: "f", Paths: []string{"/d"}, out: &directOut{t: tg, job: netdest.Job{Folder: "job", Keep: 1, Now: time.Now()}}}, root, bk, time.UTC, time.Now(), discard())
	if err == nil {
		t.Fatal("deveria falhar")
	}
	if b, _ := os.ReadFile(dir); string(b) != "agora é um arquivo" {
		t.Fatal("conteúdo alheio alterado")
	}
}
