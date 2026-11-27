package agentcore

// Pasta do backup local. Ela pode ser:
//   - um caminho ABSOLUTO do servidor, escolhido na interface (ex.: /mnt/hd2/backups). O agente grava nele através de uma
//     montagem gravável separada da origem (-v /:/host-rw); a origem continua somente leitura;
//   - um nome relativo (jobs antigos), dentro da pasta de backups do agente (/backups).

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// pastas virtuais ou voláteis do sistema, onde um backup não pode ser gravado
var forbiddenLocalDirs = []string{"/proc", "/sys", "/dev", "/run"}

// CheckLocalDir valida um caminho absoluto do servidor para receber backups e o devolve normalizado.
func CheckLocalDir(p string) (string, error) {
	if !strings.HasPrefix(p, "/") || len(p) > 1024 || strings.ContainsAny(p, "\x00\r\n") {
		return "", fmt.Errorf("pasta de backup local inválida: %q", p)
	}
	for _, s := range strings.Split(p, "/") {
		if s == ".." {
			return "", fmt.Errorf("pasta de backup local inválida: %q", p)
		}
	}
	clean := path.Clean(p)
	if clean == "/" {
		return "", errors.New("escolha uma pasta: o backup não pode ser gravado direto na raiz do agente")
	}
	for _, f := range forbiddenLocalDirs {
		if clean == f || strings.HasPrefix(clean, f+"/") {
			return "", fmt.Errorf("%s é uma pasta do sistema e não pode receber backups", clean)
		}
	}
	return clean, nil
}

var errNoWriteMount = errors.New("este agente não pode gravar fora das próprias pastas: adicione o volume \"- /:/host-rw\" à stack do agente e recrie o contêiner")

// writeRoot devolve a raiz gravável do servidor, como o agente a enxerga.
func (c *Core) writeRoot() (string, error) {
	r := c.cfg.HostWriteRoot
	if r == "" {
		return "", errNoWriteMount
	}
	if st, err := os.Stat(r); err != nil || !st.IsDir() {
		return "", errNoWriteMount
	}
	return r, nil
}

// noLinks confere que nenhum trecho existente de rel (dentro de root) é link simbólico ou arquivo: um atalho absoluto do
// servidor apontaria, dentro do contêiner, para outro lugar, e o backup se perderia.
func noLinks(root, rel string) error {
	cur := root
	for _, seg := range strings.Split(strings.Trim(rel, "/"), "/") {
		cur = filepath.Join(cur, seg)
		st, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil // daqui em diante será criado
		}
		if err != nil {
			return fmt.Errorf("sem acesso a %s", strings.TrimPrefix(cur, strings.TrimSuffix(root, "/")))
		}
		if !st.IsDir() {
			return fmt.Errorf("%s não é uma pasta (atalhos não são aceitos)", "/"+strings.TrimPrefix(strings.TrimPrefix(cur, root), "/"))
		}
	}
	return nil
}

// shownPath traduz um caminho real do agente para o caminho do servidor, que é o que o usuário conhece.
func (c *Core) shownPath(p string) string {
	r := strings.TrimSuffix(c.cfg.HostWriteRoot, "/")
	if r != "" && strings.HasPrefix(p, r+"/") {
		return strings.TrimPrefix(p, r)
	}
	return p
}

// localDir resolve a pasta local de um job para o caminho real em que o agente grava.
func (c *Core) localDir(dir string) (string, error) {
	if !strings.HasPrefix(dir, "/") {
		return SafeSubdir(c.cfg.BackupDir, dir)
	}
	clean, err := CheckLocalDir(dir)
	if err != nil {
		return "", err
	}
	if !PathAllowed(c.cfg.BackupRoots, clean) {
		return "", fmt.Errorf("este agente não permite gravar backups em %s (CORUJA_BACKUP_ROOTS)", clean)
	}
	root, err := c.writeRoot()
	if err != nil {
		return "", err
	}
	if err := noLinks(root, clean); err != nil {
		return "", err
	}
	return filepath.Join(root, clean), nil
}

// diskSpace devolve o espaço livre (para usuários) e total do sistema de arquivos de p, e se ele está somente leitura.
// used é o ocupado de fato (sem o espaço reservado ao root), como o df: o percentual em uso é used/(used+free).
func diskSpace(p string) (free, total, used int64, readOnly bool, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return 0, 0, 0, false, err
	}
	const stReadOnly = 0x1
	bs := int64(st.Bsize)
	return int64(st.Bavail) * bs, int64(st.Blocks) * bs, int64(st.Blocks-st.Bfree) * bs, st.Flags&stReadOnly != 0, nil
}

// BrowseDisk lista as pastas do servidor para a escolha da pasta do backup local, com o espaço disponível. Uma pasta que
// ainda não existe (será criada no primeiro backup) mostra o espaço do disco onde ficará.
func BrowseDisk(root, p string, allowed []string) (BrowseResult, error) {
	logical := path.Clean("/" + strings.TrimSpace(p))
	res := BrowseResult{Path: logical, Entries: []BrowseEntry{}, Exists: true}
	if strings.ContainsRune(logical, 0) {
		return res, errors.New("caminho inválido")
	}
	if !leadsToAllowed(allowed, logical) {
		return res, fmt.Errorf("este agente não permite gravar backups em %s (CORUJA_BACKUP_ROOTS)", logical)
	}
	if logical != "/" {
		if _, err := CheckLocalDir(logical); err != nil {
			return res, err
		}
		if err := noLinks(root, logical); err != nil {
			return res, err
		}
	}
	existing := logical
	for existing != "/" {
		if _, err := os.Lstat(filepath.Join(root, existing)); !errors.Is(err, fs.ErrNotExist) {
			break
		}
		existing, res.Exists = path.Dir(existing), false
	}
	free, total, used, readOnly, err := diskSpace(filepath.Join(root, existing))
	if err != nil {
		return res, fmt.Errorf("não foi possível medir o espaço em %s", existing)
	}
	res.Free, res.Total, res.Used = free, total, used
	switch {
	case logical == "/":
		res.Blocked = "Entre numa pasta: o backup não é gravado direto na raiz."
	case !PathAllowed(allowed, logical):
		res.Blocked = "Este agente não permite gravar aqui (CORUJA_BACKUP_ROOTS)."
	case readOnly:
		res.Blocked = "Este disco está montado somente leitura."
	}
	mounts := readMountInfo()
	res.Disks = listDisks(root, mounts)
	res.Mount, res.FS = mountOf(root, existing, mounts)
	if !res.Exists {
		return res, nil
	}
	list, err := Browse(root, BrowseRequest{Area: BrowseDiskArea, Path: logical}, allowed)
	if err != nil {
		return res, err
	}
	res.Entries, res.Truncated = list.Entries, list.Truncated
	return res, nil
}

// sistemas de arquivos que guardam dados de verdade (discos, partições e compartilhamentos de rede)
var realFS = map[string]bool{"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true, "vfat": true, "exfat": true,
	"ntfs": true, "ntfs3": true, "fuseblk": true, "f2fs": true, "reiserfs": true, "jfs": true, "bcachefs": true, "nfs": true, "nfs4": true,
	"cifs": true, "smb3": true, "smbfs": true, "ceph": true, "glusterfs": true, "lustre": true}

type mountEntry struct {
	Dev, Point, FS string
}

func readMountInfo() []mountEntry {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseMountInfo(f)
}

// parseMountInfo lê o formato de /proc/self/mountinfo: dispositivo (3º campo), ponto de montagem (5º) e tipo (após " - ").
func parseMountInfo(r io.Reader) []mountEntry {
	var out []mountEntry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		before, after, ok := strings.Cut(sc.Text(), " - ")
		f, t := strings.Fields(before), strings.Fields(after)
		if !ok || len(f) < 5 || len(t) < 1 {
			continue
		}
		out = append(out, mountEntry{Dev: f[2], Point: unescapeMount(f[4]), FS: t[0]})
	}
	return out
}

// listDisks devolve as partições montadas dentro de root (a máquina, como o agente a enxerga), uma por dispositivo
// (o ponto de montagem mais curto), com o espaço de cada uma.
func listDisks(root string, mounts []mountEntry) []Disk {
	root = strings.TrimSuffix(filepath.Clean(root), "/")
	best := map[string]mountEntry{}
	for _, m := range mounts {
		if !realFS[m.FS] {
			continue
		}
		if m.Point != root && !strings.HasPrefix(m.Point, root+"/") && root != "" {
			continue
		}
		if cur, ok := best[m.Dev]; !ok || len(m.Point) < len(cur.Point) {
			best[m.Dev] = m
		}
	}
	out := make([]Disk, 0, len(best))
	for _, m := range best {
		free, total, used, ro, err := diskSpace(m.Point)
		if err != nil || total == 0 {
			continue
		}
		p := strings.TrimPrefix(m.Point, root)
		if p == "" {
			p = "/"
		}
		out = append(out, Disk{Path: p, FS: m.FS, Total: total, Free: free, Used: used, RO: ro})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// mountOf devolve o ponto de montagem (como o usuário o conhece) e o tipo da partição onde fica logical: o ponto de
// montagem mais longo, dentro de root, que contém o caminho.
func mountOf(root, logical string, mounts []mountEntry) (string, string) {
	root = strings.TrimSuffix(filepath.Clean(root), "/")
	full := root + logical
	if logical == "/" {
		full = root
	}
	best, fs := "", ""
	for _, m := range mounts {
		if m.Point != root && !strings.HasPrefix(m.Point, root+"/") && root != "" {
			continue
		}
		if (full == m.Point || strings.HasPrefix(full, strings.TrimSuffix(m.Point, "/")+"/")) && len(m.Point) >= len(best) {
			best, fs = m.Point, m.FS
		}
	}
	if best == "" {
		return "", ""
	}
	p := strings.TrimPrefix(best, root)
	if p == "" {
		p = "/"
	}
	return p, fs
}
