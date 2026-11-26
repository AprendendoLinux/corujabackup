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
	if c.Listen != ":8080" || c.DataDir != "/data" || c.DBHost != "db" || c.DBName != "corujabackup" || c.DBUser != "corujabackup" || c.DBPort != "3306" {
		t.Fatalf("padrões inesperados: %+v", c)
	}
}

func TestRedactedNaoVazaSenha(t *testing.T) {
	c, _ := Load(env(map[string]string{"CORUJA_DB_PASSWORD": "segredo123"}))
	if strings.Contains(c.Redacted(), "segredo123") || !strings.Contains(c.DSN(), "segredo123") {
		t.Fatal("Redacted deve ocultar a senha e DSN deve conter")
	}
}

func TestHTTPSeTLSDoBanco(t *testing.T) {
	base := map[string]string{"CORUJA_DB_PASSWORD": "x"}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range base {
			m[a] = b
		}
		m[k] = v
		return m
	}
	if _, err := Load(env(with("CORUJA_TLS_CERT", "/c.pem"))); err == nil {
		t.Fatal("certificado sem chave deveria falhar")
	}
	if _, err := Load(env(with("CORUJA_DB_TLS", "talvez"))); err == nil {
		t.Fatal("valor inválido de CORUJA_DB_TLS")
	}
	c, err := Load(env(with("CORUJA_DB_TLS", "skip-verify")))
	if err != nil || !strings.HasSuffix(c.DSN(), "&tls=skip-verify") {
		t.Fatalf("%v %s", err, c.DSN())
	}
	if c2, _ := Load(env(base)); strings.Contains(c2.DSN(), "tls=") {
		t.Fatal("padrão: sem TLS")
	}
}
