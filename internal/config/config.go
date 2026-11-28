// Package config lê a configuração do Server a partir de variáveis de ambiente.
// Propositalmente mínima: tudo o que é operacional é configurado pela interface web.
package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Listen     string // interface web e API REST, padrão ":8080" (fica atrás do proxy reverso, se houver)
	DataDir    string // certificado TLS e dados locais, padrão "/data"
	TrustProxy bool   // confiar em X-Forwarded-For/Proto (só atrás de proxy reverso confiável)
	TLSCert    string // HTTPS direto na interface (opcional): caminho do certificado PEM
	TLSKey     string // e da chave privada PEM
	DBTLS      string // TLS com o MySQL: vazio/false = sem TLS; true = com validação; skip-verify; preferred
	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string
	// Agente local (o serviço "agent" do compose do Server): o Server o matricula sozinho. LocalAgent vazio = desligado.
	LocalAgent          string // host:porta do agente na rede interna, ex.: corujabackup-agent:23847
	LocalAgentName      string
	LocalAgentTokenFile string
}

// Load lê o ambiente. Só CORUJA_DB_PASSWORD é obrigatória.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Listen:     or(getenv("CORUJA_LISTEN"), ":8080"),
		DataDir:    or(getenv("CORUJA_DATA_DIR"), "/data"),
		TrustProxy: strings.EqualFold(getenv("CORUJA_TRUST_PROXY"), "true"),
		TLSCert:    strings.TrimSpace(getenv("CORUJA_TLS_CERT")),
		TLSKey:     strings.TrimSpace(getenv("CORUJA_TLS_KEY")),
		DBTLS:      strings.ToLower(strings.TrimSpace(getenv("CORUJA_DB_TLS"))),
		DBHost:     or(getenv("CORUJA_DB_HOST"), "db"),
		DBPort:     or(getenv("CORUJA_DB_PORT"), "3306"),
		DBName:     or(getenv("CORUJA_DB_NAME"), "corujabackup"),
		DBUser:     or(getenv("CORUJA_DB_USER"), "corujabackup"),
		DBPassword: getenv("CORUJA_DB_PASSWORD"),

		LocalAgent:          strings.TrimSpace(getenv("CORUJA_LOCAL_AGENT")),
		LocalAgentName:      or(strings.TrimSpace(getenv("CORUJA_LOCAL_AGENT_NAME")), "coruja-server"),
		LocalAgentTokenFile: or(strings.TrimSpace(getenv("CORUJA_LOCAL_AGENT_TOKEN_FILE")), "/matricula/token"),
	}
	if strings.TrimSpace(c.DBPassword) == "" {
		return c, fmt.Errorf("a variável CORUJA_DB_PASSWORD é obrigatória")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return c, fmt.Errorf("para HTTPS informe CORUJA_TLS_CERT e CORUJA_TLS_KEY juntas")
	}
	switch c.DBTLS {
	case "", "false", "true", "skip-verify", "preferred":
	default:
		return c, fmt.Errorf("CORUJA_DB_TLS inválida: use false, true, skip-verify ou preferred")
	}
	return c, nil
}

// DSN monta a string de conexão (sem expor a senha em logs: use Redacted para exibir).
func (c Config) DSN() string {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=UTC&charset=utf8mb4&collation=utf8mb4_0900_ai_ci&time_zone=%%27%%2B00%%3A00%%27",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
	if c.DBTLS != "" && c.DBTLS != "false" {
		dsn += "&tls=" + c.DBTLS
	}
	return dsn
}

func (c Config) Redacted() string {
	return fmt.Sprintf("%s:***@tcp(%s:%s)/%s", c.DBUser, c.DBHost, c.DBPort, c.DBName)
}

func Getenv(k string) string { return os.Getenv(k) }

func or(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}
