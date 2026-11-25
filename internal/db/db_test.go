package db

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestSplitSQL(t *testing.T) {
	got := splitSQL("-- c\nCREATE TABLE a (\n x INT\n);\n\nINSERT INTO a VALUES (1);\n")
	want := []string{"CREATE TABLE a (\n x INT\n);", "INSERT INTO a VALUES (1);"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

// Teste de integração: roda só se CORUJA_TEST_DSN apontar para um banco MySQL descartável.
func TestMigrateIdempotente(t *testing.T) {
	dsn := os.Getenv("CORUJA_TEST_DSN")
	if dsn == "" {
		t.Skip("CORUJA_TEST_DSN não definido")
	}
	ctx := context.Background()
	d, err := Open(ctx, dsn, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Exec("SET FOREIGN_KEY_CHECKS=0")
	for _, tb := range []string{"run_logs", "runs", "jobs", "cloud_accounts", "audit_log", "enrollment_tokens", "agents", "sessions", "users", "settings", "schema_migrations"} {
		d.Exec("DROP TABLE IF EXISTS " + tb)
	}
	d.Exec("SET FOREIGN_KEY_CHECKS=1")
	a, err := Migrate(ctx, d)
	if err != nil || len(a) == 0 {
		t.Fatalf("primeira migração: %v %v", a, err)
	}
	b, err := Migrate(ctx, d)
	if err != nil || len(b) != 0 {
		t.Fatalf("segunda migração deveria ser vazia: %v %v", b, err)
	}
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM settings WHERE k='brand.name'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("configuração padrão ausente: %v %d", err, n)
	}
}
