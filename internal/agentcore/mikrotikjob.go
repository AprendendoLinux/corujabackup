package agentcore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/mikrotik"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

// MikrotikConfigFrom converte a definição recebida do Server.
func MikrotikConfigFrom(s *pb.MikrotikSpec) mikrotik.Config {
	if s == nil {
		return mikrotik.Config{}
	}
	return mikrotik.Config{Host: s.Host, Port: int(s.Port), User: s.User, Password: s.Password, PrivateKey: s.PrivateKey, HostKey: s.HostKey,
		Export: s.Export, ExportSensitive: s.ExportSensitive, Binary: s.Binary, BackupPassword: s.BackupPassword}
}

// runMikrotik salva um roteador MikroTik: o export .rsc (com e/ou sem dados sensíveis), o .backup cifrado e um
// info.txt com a identificação, empacotados em <slug>-AAAAMMDD-HHMMSS.tar.gz e conferidos como os outros backups.
func runMikrotik(ctx context.Context, name string, spec *pb.MikrotikSpec, subdir, backupDir string, loc *time.Location, now time.Time, h Hooks, out archiveOut) (Result, error) {
	if spec == nil {
		return Result{}, errors.New("job de MikroTik sem configuração")
	}
	cfg := MikrotikConfigFrom(spec)
	if err := cfg.Normalize(); err != nil {
		return Result{}, err
	}
	if cfg.HostKey == "" {
		return Result{}, errors.New("o job não tem a impressão digital do roteador confirmada: teste e salve o job de novo")
	}
	dest, err := SafeSubdir(backupDir, subdir)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return Result{}, fmt.Errorf("não foi possível criar a pasta de destino: %w", err)
	}
	work := filepath.Join(dest, ".tmp-"+randName())
	if err := os.Mkdir(work, 0o700); err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(work) // tem o .backup e, talvez, o export com senhas: nada fica para trás

	h.log("info", "conectando ao roteador %s:%d", cfg.Host, cfg.Port)
	r, err := mikrotik.Dial(ctx, cfg)
	if err != nil {
		return Result{}, err
	}
	defer r.Close()
	info, err := r.Info()
	if err != nil {
		return Result{}, err
	}
	h.log("info", "roteador %s: RouterOS %s, %s", info.Identity, info.Version, info.Board)
	var files, total int64
	save := func(fname string, data []byte) error {
		if err := os.WriteFile(filepath.Join(work, fname), data, 0o600); err != nil {
			return err
		}
		files++
		total += int64(len(data))
		return nil
	}
	if cfg.Export {
		b, err := r.Export(info, false)
		if err != nil {
			return Result{}, fmt.Errorf("export: %w", err)
		}
		if err := save("config.rsc", b); err != nil {
			return Result{}, err
		}
		h.log("info", "export da configuração: config.rsc (%s)", HumanBytes(int64(len(b))))
	}
	if cfg.ExportSensitive {
		b, err := r.Export(info, true)
		if err != nil {
			return Result{}, fmt.Errorf("export com dados sensíveis: %w", err)
		}
		if err := save("config-com-dados-sensiveis.rsc", b); err != nil {
			return Result{}, err
		}
		h.log("info", "export com dados sensíveis: config-com-dados-sensiveis.rsc (%s; contém senhas e chaves)", HumanBytes(int64(len(b))))
	}
	if cfg.Binary {
		var buf bytes.Buffer
		n, err := r.Backup(ctx, cfg.BackupPassword, &buf)
		if err != nil {
			return Result{}, err
		}
		bname := Slug(info.Identity)
		if bname == "" {
			bname = "roteador"
		}
		if err := save(bname+".backup", buf.Bytes()); err != nil {
			return Result{}, err
		}
		h.log("info", "backup binário: %s.backup (%s, cifrado com a senha do job; apagado do roteador)", bname, HumanBytes(n))
	}
	var txt strings.Builder
	fmt.Fprintf(&txt, "Roteador: %s\nRouterOS: %s\nModelo: %s\nArquitetura: %s\n", info.Identity, info.Version, info.Board, info.Arch)
	if info.Serial != "" {
		fmt.Fprintf(&txt, "Número de série: %s\n", info.Serial)
	}
	fmt.Fprintf(&txt, "Endereço: %s:%d\nChave SSH: %s\nData: %s\n\nRestauração:\n", cfg.Host, cfg.Port, info.HostKey, now.In(loc).Format("02/01/2006 15:04:05 MST"))
	txt.WriteString("- config.rsc / config-com-dados-sensiveis.rsc: o export é a configuração COMPLETA, para aplicar num roteador ZERADO\n")
	txt.WriteString("  (por cima de uma configuração existente, o /import para no primeiro item repetido). Envie o arquivo ao roteador e:\n")
	txt.WriteString("    /system reset-configuration no-defaults=yes skip-backup=yes run-after-reset=<arquivo>.rsc\n")
	txt.WriteString("  Se as interfaces ainda não existirem no boot, acrescente \":delay 15s\" na primeira linha do arquivo.\n")
	txt.WriteString("  Num roteador já zerado (sem configuração), basta /import file-name=<arquivo>.rsc\n")
	txt.WriteString("- .backup: envie ao roteador e rode /system backup load name=<arquivo>.backup password=<senha do job>\n")
	txt.WriteString("  (só restaura no mesmo modelo de equipamento; inclui usuários e certificados)\n")
	if err := save("info.txt", []byte(txt.String())); err != nil {
		return Result{}, err
	}
	return packWork(ctx, work, dest, Slug(name), loc, now, h, out, files, total, "arquivos do roteador")
}
