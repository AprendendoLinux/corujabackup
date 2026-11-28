package netdest

// Backups em pasta (0.0.29): cada execução vira uma pasta com a árvore original, arquivo por arquivo, gravada com nome
// provisório (<nome>.coruja-part) e renomeada só no fim, depois de tudo conferido. O que não mudou desde a pasta
// anterior vira um link físico para o arquivo dela, onde o storage permite (pasta local, NFS, SFTP com OpenSSH).

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/bkname"
)

// PartSuffix marca o que ainda está sendo gravado (arquivo ou pasta).
const PartSuffix = partSuffix

// Linker: storages que criam links físicos (from e to relativos à pasta base, como nos outros métodos).
type Linker interface{ Link(from, to string) error }

// Symlinker: storages que guardam links simbólicos.
type Symlinker interface {
	Symlink(name, target string) error
}

// Chmoder: storages que aceitam trocar as permissões (a pasta de cada execução fica só com o dono).
type Chmoder interface {
	Chmod(name string, mode fs.FileMode) error
}

// AttrSetter: storages que guardam dono, permissões e data originais (a pasta local).
type AttrSetter interface {
	SetAttrs(name string, mode fs.FileMode, uid, gid int, mtime time.Time)
}

// LocalTarget abre uma pasta da máquina do agente (job sem storage) para receber backups em pasta.
func LocalTarget(dir string) (*Target, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("não foi possível criar a pasta de destino: %w", err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("não foi possível abrir a pasta de destino: %w", err)
	}
	return &Target{ctx: context.Background(), c: Config{Type: TypeLocal, Verify: VerifyFull}, conn: &localConn{root: r, dir: dir}}, nil
}

// Conn é a conexão aberta com o storage.
func (t *Target) Conn() Conn { return t.conn }

// Folder é a pasta do job no storage.
func (t *Target) Folder() string { return t.folder }

// Type é o tipo do storage.
func (t *Target) Type() string { return t.c.Type }

// FullVerify diz se cada arquivo gravado é relido do storage (senão, só o tamanho é conferido).
func (t *Target) FullVerify() bool { return t.c.Verify == VerifyFull }

// Parallel é quantos arquivos gravar ao mesmo tempo: FTP e SMB, um por vez (uma conexão só para tudo).
func (t *Target) Parallel() int {
	if t.c.Type == TypeFTP || t.c.Type == TypeSMB {
		return 1
	}
	return 4
}

// ExistsDir informa se há uma pasta com esse nome na pasta do job.
func (t *Target) ExistsDir(name string) bool {
	ds, err := t.conn.Dirs(t.folder)
	if err != nil {
		return false
	}
	for _, d := range ds {
		if d == name {
			return true
		}
	}
	return false
}

// RemoveTree apaga uma pasta da pasta do job. Se o backup foi cancelado (a conexão cai junto), usa uma conexão nova.
func (t *Target) RemoveTree(name string) error {
	if name == "" || strings.Contains(name, "/") || name == "." || name == ".." {
		return errors.New("pasta inválida")
	}
	err := t.conn.RemoveAll(path.Join(t.folder, name))
	if err == nil || t.ctx.Err() == nil {
		return err
	}
	// a conexão antiga ainda pode segurar o arquivo que estava sendo gravado (no SMB, ninguém mais consegue apagá-lo):
	// fecha-a antes, e a nova passa a ser a do Target
	_ = t.conn.Close()
	cctx, cancel := context.WithTimeout(context.WithoutCancel(t.ctx), 2*time.Minute)
	defer cancel()
	c2, _, derr := Dial(cctx, t.c, t.env)
	if derr != nil {
		return err
	}
	t.conn = c2
	for i := 0; ; i++ { // o servidor pode levar um instante para soltar os arquivos da conexão fechada
		if err = c2.RemoveAll(path.Join(t.folder, name)); err == nil || i == 4 {
			return err
		}
		time.Sleep(time.Second)
	}
}

// SweepParts apaga as pastas provisórias deste job deixadas por execuções interrompidas (o agente parou no meio).
func (t *Target) SweepParts(prefix string) []string {
	ds, err := t.conn.Dirs(t.folder)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range ds {
		name := strings.TrimSuffix(d, partSuffix)
		if name == d {
			continue
		}
		if _, _, ok := bkname.Parse(name, prefix); ok && t.conn.RemoveAll(path.Join(t.folder, d)) == nil {
			out = append(out, d)
		}
	}
	return out
}

// Retain aplica a retenção na pasta do job: as pastas de backup e os .tar.gz antigos do mesmo job (de quando o formato
// era outro) contam juntos. current nunca é apagado. Devolve as linhas para o log.
func (t *Target) Retain(prefix string, keep, keepDays int, now time.Time, current string) []string {
	files, err := t.conn.List(t.folder)
	if err != nil {
		return []string{"retenção ignorada: não foi possível listar a pasta: " + err.Error()}
	}
	dirs, err := t.conn.Dirs(t.folder)
	if err != nil {
		return []string{"retenção ignorada: não foi possível listar a pasta: " + err.Error()}
	}
	all := make([]bkname.File, 0, len(files)+len(dirs))
	isDir := map[string]bool{}
	for _, f := range files {
		all = append(all, bkname.File{Name: f.Name, Modified: f.Modified})
	}
	for _, d := range dirs {
		if bkname.IsDir(d) {
			all = append(all, bkname.File{Name: d})
			isDir[d] = true
		}
	}
	var out []string
	for _, old := range bkname.Retention(all, prefix, keep, keepDays, now) {
		if old.Name == current {
			continue
		}
		var err error
		if isDir[old.Name] {
			err = t.conn.RemoveAll(path.Join(t.folder, old.Name))
		} else {
			err = t.conn.Remove(t.folder, old.Name)
		}
		if err != nil {
			out = append(out, "não foi possível apagar "+old.Name+": "+err.Error())
			continue
		}
		out = append(out, "removido do destino: "+old.Name)
	}
	return out
}
