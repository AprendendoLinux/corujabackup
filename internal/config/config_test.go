package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestSenhaObrigatoria(t *testing.T) {
	if _, err := Load(env(nil)); err == nil || !strings.Contains(err.Error(), "CORUJA_DB_PASSWORD") {
		t.Fatalf("esperava erro pedindo a senha, veio %v", err)
	}
}

func TestPadroes(t *testing.T) {
	c, err := Load(env(map[string]string{"CORUJA_DB_PASSWORD": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.AgentListen != ":8443" || c.DataDir != "/data" || c.DBHost != "db" || c.DBName != "corujabackup" || c.DBUser != "corujabackup" || c.DBPort != "3306" {
		t.Fatalf("padrões inesperados: %+v", c)
	}
}

func TestRedactedNaoVazaSenha(t *testing.T) {
	c, _ := Load(env(map[string]string{"CORUJA_DB_PASSWORD": "segredo123"}))
	if strings.Contains(c.Redacted(), "segredo123") || !strings.Contains(c.DSN(), "segredo123") {
		t.Fatal("Redacted deve ocultar a senha e DSN deve conter")
	}
}
