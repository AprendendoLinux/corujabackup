package agentcore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

// conteúdo pouco compressível (para os blocos encherem com poucos arquivos)
func noise(n int, seed int64) string {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return string(b)
}

// logSink junta as linhas de log das ações (o que a interface mostraria).
func logSink(c *Core) func() string {
	var mu sync.Mutex
	var sb strings.Builder
	c.SetSender(func(m *pb.AgentMessage) bool {
		if al := m.GetActionLog(); al != nil {
			mu.Lock()
			for _, l := range al.Logs {
				sb.WriteString(l.Text + "\n")
			}
			mu.Unlock()
		}
		return true
	})
	return func() string { mu.Lock(); defer mu.Unlock(); return sb.String() }
}

// Backup em blocos: o .tar.gz continua comum (lido de ponta a ponta), o índice confere com o arquivo, a restauração de
// um arquivo lê só o bloco dele, um bloco adulterado é recusado e um backup sem índice é lido em fluxo, parando ao
// achar tudo. No incremental, o que não mudou continua apontando para o bloco do full.
func TestBackupEmBlocosERestauracaoSeletiva(t *testing.T) {
	old := blockTarget
	blockTarget = 4 << 10
	defer func() { blockTarget = old }()
	root, bk := t.TempDir(), t.TempDir()
	st, _ := NewState(t.TempDir())
	c := New(Config{HostRoot: root, HostWriteRoot: root, BackupDir: bk}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	logs := logSink(c)
	want := map[string]string{}
	for i := 0; i < 40; i++ {
		rel := filepath.Join("srv", "d"+string(rune('a'+i%4)), "f"+string(rune('A'+i))+".bin")
		want[rel] = noise(3000, int64(i))
		write(t, filepath.Join(root, rel), want[rel])
	}
	ctx := context.Background()
	base := time.Date(2026, 11, 28, 3, 0, 0, 0, time.UTC)
	job := FilesJob{Name: "Blk", Paths: []string{"/srv"}, Subdir: "blk", Keep: 10}
	j := job
	j.inc = &incPlan{}
	res, err := RunFiles(ctx, j, root, bk, time.UTC, base, discard())
	if err != nil {
		t.Fatal(err)
	}
	name := res.OutputName
	if err := st.SaveCatalog(1, name, res.Snapshot); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(bk, "blk", name)
	raw, _ := os.ReadFile(archive)

	// índice: blocos colados, do começo ao fim do arquivo, cada um com o SHA-256 certo
	bl := res.Snapshot.Blocks[name]
	if len(bl) < 10 {
		t.Fatalf("deveria haver vários blocos: %d", len(bl))
	}
	for k, b := range bl {
		if (k == 0 && b.O != 0) || (k > 0 && b.O != bl[k-1].E) || b.E <= b.O {
			t.Fatalf("bloco %d fora de ordem: %+v", k, b)
		}
		if s := sha256.Sum256(raw[b.O:b.E]); hex.EncodeToString(s[:]) != b.H {
			t.Fatalf("bloco %d com SHA-256 errado", k)
		}
	}
	if bl[len(bl)-1].E != int64(len(raw)) || res.SHA256 == "" {
		t.Fatalf("o último bloco termina no fim do arquivo: %d x %d", bl[len(bl)-1].E, len(raw))
	}
	for p, m := range res.Snapshot.Files {
		if m.B < 1 || m.B > len(bl) {
			t.Fatalf("%s sem bloco válido: %+v", p, m)
		}
	}
	// continua um .tar.gz comum, lido de ponta a ponta
	if _, _, _, err := extractOne(ctx, archive, filepath.Join(t.TempDir(), "tudo"), func(string, string, ...any) {}); err != nil {
		t.Fatal(err)
	}

	restore := func(target string, paths ...string) error {
		act := &pb.AgentAction{Kind: "restore_files", JobId: 1, FileName: name, Subdir: "blk", Paths: paths, Target: target, Sha256: res.SHA256}
		return c.restoreFiles(ctx, act, &actionLog{c: c, act: act})
	}
	// um arquivo só: só o bloco dele
	one := filepath.Join("srv", "db", "fF.bin")
	if err := restore("um", "/"+one); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(bk, "restores", "um", one)); string(b) != want[one] {
		t.Fatal("conteúdo restaurado diferente")
	}
	if l := logs(); !strings.Contains(l, "só 1 de ") {
		t.Fatalf("deveria ler só 1 bloco:\n%s", l)
	}
	// uma pasta inteira: os blocos dela, e tudo certo
	if err := restore("pasta", "/srv/dc"); err != nil {
		t.Fatal(err)
	}
	got := tree(t, filepath.Join(bk, "restores", "pasta"))
	n := 0
	for rel, content := range want {
		if strings.HasPrefix(rel, "srv/dc/") {
			n++
			if got[rel] != content {
				t.Fatalf("%s diferente", rel)
			}
		}
	}
	if n != 10 || len(got) != 12 { // 10 arquivos + srv/ e srv/dc/
		t.Fatalf("restauração da pasta: %d arquivos, %v", n, len(got))
	}

	// bloco adulterado: recusa, e a pasta de restauração some
	blk := bl[res.Snapshot.Files["/"+one].B-1]
	bad := bytes.Clone(raw)
	bad[blk.O+(blk.E-blk.O)/2] ^= 0xff
	os.WriteFile(archive, bad, 0o600)
	if err := restore("adulterado", "/"+one); err == nil || !strings.Contains(err.Error(), "não confere com o catálogo") {
		t.Fatalf("bloco adulterado deveria ser recusado: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bk, "restores", "adulterado")); err == nil {
		t.Fatal("a pasta de restauração não deveria ficar")
	}
	os.WriteFile(archive, raw, 0o600)

	// backup sem índice (anterior à 0.0.28): lido em fluxo, parando ao achar tudo
	noIdx := *res.Snapshot
	noIdx.Blocks = nil
	st.SaveCatalog(1, name, &noIdx)
	catCache = &catalogCache{items: map[string]*Snapshot{}}
	first := ""
	for p, m := range res.Snapshot.Files { // um item do primeiro bloco: a leitura para logo
		if m.B == 1 && !os.FileMode(m.Mode).IsDir() {
			first = p
		}
	}
	if err := restore("fluxo", first); err != nil {
		t.Fatal(err)
	}
	if l := logs(); !strings.Contains(l, "em fluxo") || !strings.Contains(l, "parou antes do fim") {
		t.Fatalf("leitura em fluxo:\n%s", l)
	}
	if b, _ := os.ReadFile(filepath.Join(bk, "restores", "fluxo", first)); string(b) != want[strings.TrimPrefix(first, "/")] {
		t.Fatal("conteúdo da leitura em fluxo")
	}
	st.SaveCatalog(1, name, res.Snapshot)
	catCache = &catalogCache{items: map[string]*Snapshot{}}

	// incremental: o que não mudou aponta para o bloco do full; o índice dos dois arquivos fica no catálogo
	time.Sleep(10 * time.Millisecond)
	changed := filepath.Join("srv", "da", "fA.bin")
	write(t, filepath.Join(root, changed), "mudou")
	j.inc = &incPlan{base: res.Snapshot}
	res2, err := RunFiles(ctx, j, root, bk, time.UTC, base.Add(24*time.Hour), discard())
	if err != nil {
		t.Fatal(err)
	}
	st.SaveCatalog(1, res2.OutputName, res2.Snapshot)
	if len(res2.Snapshot.Blocks) != 2 || res2.Snapshot.Files["/"+one].A != 0 || res2.Snapshot.Files["/"+one].B != res.Snapshot.Files["/"+one].B {
		t.Fatalf("incremental: blocos %d, item %+v", len(res2.Snapshot.Blocks), res2.Snapshot.Files["/"+one])
	}
	act := &pb.AgentAction{Kind: "restore_files", JobId: 1, FileName: res2.OutputName, Subdir: "blk", Paths: []string{"/" + one, "/" + changed}, Target: "inc"}
	if err := c.restoreFiles(ctx, act, &actionLog{c: c, act: act}); err != nil {
		t.Fatal(err)
	}
	out := tree(t, filepath.Join(bk, "restores", "inc"))
	if out[one] != want[one] || out[changed] != "mudou" {
		t.Fatalf("restauração do incremental: %d itens", len(out))
	}
}

// Bloco grande: conferido num temporário (apagado depois); bloco errado não entrega nenhum byte.
func TestBlocoGrandeConferidoEmTemporario(t *testing.T) {
	old := memBlock
	memBlock = 16
	defer func() { memBlock = old }()
	data := []byte(noise(100, 7) + noise(80, 8))
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	br := blockRange{{O: 10, E: 110, H: sum(data[:100])}, {O: 110, E: 190, H: sum(data[100:])}}
	tmp := t.TempDir()
	vb := newVerifiedBlocks(bytes.NewReader(data), br, tmp)
	got, err := io.ReadAll(vb)
	vb.Close()
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("leitura conferida: %v", err)
	}
	if ents, _ := os.ReadDir(tmp); len(ents) != 0 {
		t.Fatalf("temporários sobrando: %v", ents)
	}
	bad := bytes.Clone(data)
	bad[50] ^= 1
	vb = newVerifiedBlocks(bytes.NewReader(bad), br, tmp)
	got, err = io.ReadAll(vb)
	vb.Close()
	if err == nil || len(got) != 0 || !strings.Contains(err.Error(), "não confere") {
		t.Fatalf("bloco errado: %d bytes, %v", len(got), err)
	}
	vb = newVerifiedBlocks(bytes.NewReader(data[:150]), br, tmp)
	if _, err = io.ReadAll(vb); err == nil || !strings.Contains(err.Error(), "menor") {
		t.Fatalf("trecho curto: %v", err)
	}
	vb.Close()
	if ents, _ := os.ReadDir(tmp); len(ents) != 0 {
		t.Fatalf("temporários sobrando: %v", ents)
	}
}
