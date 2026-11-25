// Package config lê a configuração do Server a partir de variáveis de ambiente.
// Propositalmente mínima: tudo o que é operacional é configurado pela interface web.
package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Listen     string // endereço de escuta HTTP/gRPC, padrão ":8080"
	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string
}

// Load lê o ambiente. Só CORUJA_DB_PASSWORD é obrigatória.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Listen:     or(getenv("CORUJA_LISTEN"), ":8080"),
		DBHost:     or(getenv("CORUJA_DB_HOST"), "db"),
		DBPort:     or(getenv("CORUJA_DB_PORT"), "3306"),
		DBName:     or(getenv("CORUJA_DB_NAME"), "corujabackup"),
		DBUser:     or(getenv("CORUJA_DB_USER"), "corujabackup"),
		DBPassword: getenv("CORUJA_DB_PASSWORD"),
	}
	if strings.TrimSpace(c.DBPassword) == "" {
		return c, fmt.Errorf("a variável CORUJA_DB_PASSWORD é obrigatória")
	}
	return c, nil
}

// DSN monta a string de conexão (sem expor a senha em logs: use Redacted para exibir).
func (c Config) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=UTC&charset=utf8mb4&collation=utf8mb4_0900_ai_ci",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
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
