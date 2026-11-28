package localagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/enroll"
)

type fakeStore struct {
	agents  map[string]bool
	pending map[string]string // nome -> hash do token
	addr    string
}

func (f *fakeStore) AgentNameExists(_ context.Context, name string) (bool, error) {
	return f.agents[name], nil
}
func (f *fakeStore) ReplacePendingToken(_ context.Context, h, name, addr string, _ time.Time) error {
	f.pending[name], f.addr = h, addr
	return nil
}

// O token entregue ao agente é válido, leva a impressão digital do Server e só o hash fica no banco; com o agente já
// matriculado, nada é gerado e o arquivo some; desligado (sem endereço), não faz nada.
func TestAgenteLocal(t *testing.T) {
	ctx := context.Background()
	fp := strings.Repeat("ab", 32)
	file := filepath.Join(t.TempDir(), "token")
	st := &fakeStore{agents: map[string]bool{}, pending: map[string]string{}}
	c := Config{Address: "corujabackup-agent:23847", Name: "coruja-server", TokenFile: file}

	if ok, err := Ensure(ctx, st, fp, Config{}); ok || err != nil {
		t.Fatalf("sem endereço deveria ficar desligado: %v %v", ok, err)
	}
	ok, err := Ensure(ctx, st, fp, c)
	if err != nil || !ok {
		t.Fatalf("deveria preparar a matrícula: %v %v", ok, err)
	}
	b, _ := os.ReadFile(file)
	tk, err := enroll.Parse(string(b))
	if err != nil || tk.Fingerprint != fp[:32] || st.pending["coruja-server"] != tk.Hash() || st.addr != c.Address {
		t.Fatalf("token entregue: %v %+v %+v", err, tk, st)
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o640 {
		t.Fatalf("permissão do arquivo: %v", fi.Mode())
	}
	if ents, _ := os.ReadDir(filepath.Dir(file)); len(ents) != 1 {
		t.Fatalf("sobrou temporário: %v", ents)
	}
	// nova subida antes de matricular: token trocado (o antigo deixa de valer)
	old := st.pending["coruja-server"]
	if ok, _ := Ensure(ctx, st, fp, c); !ok || st.pending["coruja-server"] == old {
		t.Fatal("cada subida sem matrícula renova o token")
	}
	// matriculado: não gera token e apaga o arquivo
	st.agents["coruja-server"] = true
	if ok, err := Ensure(ctx, st, fp, c); ok || err != nil {
		t.Fatalf("já matriculado: %v %v", ok, err)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("o arquivo do token deveria ter sido apagado")
	}
	if _, err := Ensure(ctx, st, fp, Config{Address: "x:1", Name: "nome inválido!", TokenFile: file}); err == nil {
		t.Fatal("nome inválido deveria falhar")
	}
}
