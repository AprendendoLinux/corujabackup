package agentcore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/mikrotik"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

// TestJobMikrotikReal: CORUJA_TEST_MIKROTIK=host e CORUJA_TEST_MIKROTIK_SENHA (usuário "coruja"), como em internal/mikrotik.
func TestJobMikrotikReal(t *testing.T) {
	host := os.Getenv("CORUJA_TEST_MIKROTIK")
	if host == "" {
		t.Skip("defina CORUJA_TEST_MIKROTIK")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	spec := &pb.MikrotikSpec{Host: host, User: "coruja", Password: os.Getenv("CORUJA_TEST_MIKROTIK_SENHA"), Export: true, ExportSensitive: true, Binary: true, BackupPassword: "senha-do-backup-1"}
	if _, err := runMikrotik(ctx, "Roteador", spec, "rt", t.TempDir(), time.UTC, time.Now(), discard(), nil); err == nil || !strings.Contains(err.Error(), "impressão digital") {
		t.Fatalf("sem impressão digital confirmada não pode rodar: %v", err)
	}
	info, err := mikrotik.Test(ctx, MikrotikConfigFrom(spec))
	if err != nil {
		t.Fatal(err)
	}
	spec.HostKey = info.HostKey
	bk := t.TempDir()
	var logs []string
	h := Hooks{Logf: func(l, f string, a ...any) { logs = append(logs, l+": "+f) }}
	res, err := runMikrotik(ctx, "Roteador", spec, "rt", bk, time.UTC, time.Now(), h, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 4 || res.OutputPath == "" || res.SHA256 == "" {
		t.Fatalf("resultado: %+v", res)
	}
	f, _ := os.Open(res.OutputPath)
	defer f.Close()
	gz, _ := gzip.NewReader(f)
	tr := tar.NewReader(gz)
	var names []string
	var sens, plain string
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hd.Name)
		b, _ := io.ReadAll(tr)
		switch filepath.Base(hd.Name) {
		case "config.rsc":
			plain = string(b)
		case "config-com-dados-sensiveis.rsc":
			sens = string(b)
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "roteador/chr-coruja-teste.backup,roteador/config-com-dados-sensiveis.rsc,roteador/config.rsc,roteador/info.txt" {
		t.Fatalf("conteúdo: %v", names)
	}
	if strings.Contains(plain, "SegredoPPP123") || !strings.Contains(sens, "SegredoPPP123") {
		t.Fatal("dados sensíveis só no export com dados sensíveis")
	}
	ents, _ := os.ReadDir(filepath.Join(bk, "rt"))
	if len(ents) != 1 {
		t.Fatalf("na pasta do job deve ficar só o .tar.gz (sem a pasta de trabalho com senhas): %v", ents)
	}
	t.Logf("%s (%d bytes); log: %v", res.OutputName, res.BytesOutput, logs)
}
