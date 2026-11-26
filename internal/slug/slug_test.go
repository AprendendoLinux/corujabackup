package slug

import "testing"

func TestOf(t *testing.T) {
	for in, want := range map[string]string{"Relatório diário": "relatorio-diario", "Home dos usuários": "home-dos-usuarios", "  --Ação & Coração!! ": "acao-coracao", "": "backup", "***": "backup", "MySQL 8.0": "mysql-8-0"} {
		if got := Of(in); got != want {
			t.Errorf("Of(%q) = %q, esperado %q", in, got, want)
		}
	}
}
