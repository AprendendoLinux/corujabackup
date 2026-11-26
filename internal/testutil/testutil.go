// Package testutil prepara um banco MySQL limpo para os testes de integração (CORUJA_TEST_DSN).
// Rode com `go test -p 1 ./...`: os pacotes compartilham o mesmo banco de testes.
package testutil

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/db"
)

func DB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CORUJA_TEST_DSN")
	if dsn == "" {
		t.Skip("CORUJA_TEST_DSN não definido (teste de integração)")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, dsn, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	conn, err := d.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0")
	for _, tb := range []string{"notifications", "notification_rules", "notification_channels", "run_logs", "runs", "jobs", "cloud_accounts", "audit_log", "enrollment_tokens", "agents", "sessions", "users", "settings", "schema_migrations"} {
		conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+tb)
	}
	conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1")
	if _, err := db.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	return d
}
