package agentcore

// Backup incremental: o agente guarda, por job, o retrato (snapshot) dos arquivos do último backup que terminou bem
// (tamanho, data, ctime, inode e dispositivo de cada item). No backup seguinte, só entram no .tar.gz os arquivos novos ou
// alterados, mais um manifesto com o que foi apagado. O retrato só é atualizado quando a execução termina bem (arquivo
// conferido e, se houver, storage conferido): se algo falhar, o próximo incremental parte do retrato anterior e leva de
// novo tudo o que mudou desde ele. Arquivos que não puderam ser lidos (ou que mudaram durante a leitura) ficam fora do
// retrato, para irem no próximo backup.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	ModeFull        = "full"
	ModeIncremental = "incremental"
	manifestName    = ".coruja-manifest.json"
	snapshotVersion = 2 // 2: cada item sabe em qual arquivo da cadeia está o conteúdo (catálogo)
)

// fileMeta é o que identifica a versão de um item.
type fileMeta struct {
	Size  int64  `json:"s,omitempty"`
	MTime int64  `json:"m,omitempty"` // ns
	CTime int64  `json:"c,omitempty"` // ns (muda também com chmod/chown)
	Ino   uint64 `json:"i,omitempty"`
	Dev   uint64 `json:"d,omitempty"`
	Mode  uint32 `json:"t,omitempty"`
	A     int    `json:"a,omitempty"` // índice em Snapshot.Archives do .tar.gz que guarda o conteúdo
	B     int    `json:"b,omitempty"` // bloco desse .tar.gz onde o item começa (1 = o primeiro; 0 = sem índice de blocos)
	// backups em pasta (0.0.29): o SHA-256 do conteúdo, dono e destino do link simbólico (o storage pode não guardá-los)
	H string `json:"h,omitempty"`
	U int    `json:"u,omitempty"`
	G int    `json:"g,omitempty"`
	L string `json:"l,omitempty"`
}

// Block é um bloco gzip independente de um .tar.gz (0.0.28): começa e termina na divisa entre itens do tar, então dá
// para ler só ele (um trecho do arquivo) e ainda ter um tar válido. H é o SHA-256 dos bytes comprimidos do bloco.
type Block struct {
	O int64  `json:"o"` // início no arquivo
	E int64  `json:"e"` // fim (exclusivo)
	H string `json:"h"`
}

// sameVersion compara a versão de dois itens (sem olhar onde o conteúdo está guardado).
func sameVersion(a, b fileMeta) bool {
	a.A, b.A, a.B, b.B = 0, 0, 0, 0
	a.H, b.H, a.U, b.U, a.G, b.G, a.L, b.L = "", "", 0, 0, 0, 0, "", "" // dono e link mudam o ctime
	return a == b
}

func metaOf(info fs.FileInfo) fileMeta {
	m := fileMeta{Size: info.Size(), MTime: info.ModTime().UnixNano(), Mode: uint32(info.Mode())}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		m.Ino, m.Dev = st.Ino, uint64(st.Dev)
		m.CTime = time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec)).UnixNano()
	}
	if info.IsDir() {
		m.Size = 0
	}
	return m
}

// Snapshot é o retrato dos arquivos do último backup que terminou bem.
type Snapshot struct {
	Version int    `json:"version"`
	Sig     string `json:"sig"`      // origens, exclusões e raiz: se mudarem, o próximo é full
	Chain   string `json:"chain"`    // nome do full que abriu a cadeia
	FullDay string `json:"full_day"` // dia (AAAA-MM-DD, fuso do job) desse full
	Last    string `json:"last"`     // último arquivo da cadeia
	// Archives são os .tar.gz da cadeia até este backup, em ordem (o full primeiro): Files[...].A aponta para eles.
	Archives []string            `json:"archives"`
	Files    map[string]fileMeta `json:"files"` // caminho lógico → versão
	// Blocks: por arquivo da cadeia, os blocos gzip dele (só os gerados a partir da 0.0.28; os antigos não têm índice).
	Blocks map[string][]Block `json:"blocks,omitempty"`
}

// Manifest vai dentro de cada .tar.gz de um job incremental (último item, na raiz).
type Manifest struct {
	Format  int      `json:"format"`
	Kind    string   `json:"kind"`  // full | incremental
	Chain   string   `json:"chain"` // nome do full da cadeia
	Base    string   `json:"base,omitempty"`
	Created string   `json:"created"`
	Deleted []string `json:"deleted,omitempty"` // caminhos (sem "/" inicial) que existiam no backup anterior e sumiram
}

// incPlan é o que RunFiles precisa para um job incremental.
type incPlan struct {
	base  *Snapshot // nil = full
	next  *Snapshot // preenchido durante a varredura
	day   string
	reads int // arquivos alterados gravados
	same  int // arquivos sem mudança (deixados de fora)
}

// current é o índice, na cadeia, do arquivo que está sendo gerado.
func (p *incPlan) current() int {
	if p.base == nil {
		return 0
	}
	return len(p.base.Archives)
}

func (p *incPlan) kind() string {
	if p.base == nil {
		return ModeFull
	}
	return ModeIncremental
}

// unchanged diz se o item continua igual ao do retrato anterior (e pode ficar fora do incremental).
func (p *incPlan) unchanged(logical string, m fileMeta) bool {
	if p.base == nil {
		return false
	}
	old, ok := p.base.Files[logical]
	return ok && sameVersion(old, m)
}

// archiveOf diz onde está o conteúdo de um item sem mudança (o mesmo do backup anterior): o arquivo da cadeia e o bloco.
func (p *incPlan) archiveOf(logical string) (int, int) {
	if p.base == nil {
		return 0, 0
	}
	m := p.base.Files[logical]
	return m.A, m.B
}

// jobSig resume o que define o conteúdo de um job: mudou, o próximo backup é full.
func jobSig(hostRoot string, paths, excludes []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00", hostRoot)
	for _, p := range paths {
		fmt.Fprintf(h, "p:%s\x00", p)
	}
	for _, e := range excludes {
		fmt.Fprintf(h, "e:%s\x00", e)
	}
	return hex.EncodeToString(h.Sum(nil))[:24]
}

// needFull decide se a execução de um job incremental deve ser full, e por quê.
func needFull(snap *Snapshot, sig string, now time.Time, fullWeekday int, force bool) (bool, string) {
	today := now.Format("2006-01-02")
	switch {
	case force:
		return true, "full pedido manualmente"
	case snap == nil:
		return true, "primeiro backup do job (sem backup anterior para comparar)"
	case snap.Sig != sig:
		return true, "as origens ou exclusões do job mudaram"
	case int(now.Weekday()) == fullWeekday && snap.FullDay != today:
		return true, "dia do backup full"
	}
	if d, err := time.ParseInLocation("2006-01-02", snap.FullDay, now.Location()); err == nil && now.Sub(d) >= 7*24*time.Hour {
		return true, "o último full tem 7 dias ou mais (o do dia marcado não rodou)"
	}
	return false, ""
}

func (s *State) snapshotPath(jobID int64) string {
	return filepath.Join(s.dir, "snapshots", strconv.FormatInt(jobID, 10)+".json.gz")
}

// LoadSnapshot devolve o retrato do job (nil se não houver, estiver ilegível ou for de formato antigo: o próximo
// backup é full).
func (s *State) LoadSnapshot(jobID int64) *Snapshot {
	snap, err := readSnapshotFile(s.snapshotPath(jobID))
	if err != nil {
		return nil
	}
	return snap
}

// SaveSnapshot grava o retrato de forma atômica (permissão 600: lista os nomes dos arquivos do servidor).
func (s *State) SaveSnapshot(jobID int64, snap *Snapshot) error {
	return writeSnapshotFile(s.snapshotPath(jobID), snap)
}

func jsonEncode(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }
func jsonDecode(r io.Reader, v any) error { return json.NewDecoder(r).Decode(v) }

// DropSnapshot esquece o retrato (o próximo backup do job é full).
func (s *State) DropSnapshot(jobID int64) { _ = os.Remove(s.snapshotPath(jobID)) }

// deletedSince lista (ordenado) o que estava no retrato anterior e não está no novo.
func deletedSince(base, next *Snapshot) []string {
	if base == nil {
		return nil
	}
	var out []string
	for p := range base.Files {
		if _, ok := next.Files[p]; !ok {
			out = append(out, strings.TrimPrefix(p, "/"))
		}
	}
	sort.Strings(out)
	return out
}
