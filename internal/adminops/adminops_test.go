package adminops

import (
	"context"
	"testing"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/store"
	"github.com/AprendendoLinux/corujabackup/internal/testutil"
)

func TestResetDeSenhaCriacaoDeAdminERecusas(t *testing.T) {
	st := store.New(testutil.DB(t))
	ctx := context.Background()
	if err := CreateAdmin(ctx, st, "Admin1", "senha-antiga-123"); err != nil {
		t.Fatal(err)
	}
	if err := CreateAdmin(ctx, st, "admin1", "outra-senha-123"); err == nil {
		t.Fatal("usuário repetido")
	}
	if err := CreateAdmin(ctx, st, "x", "senha-antiga-123"); err == nil || CreateAdmin(ctx, st, "novo.admin", "curta") == nil {
		t.Fatal("validações")
	}
	u, _ := st.UserByUsername(ctx, "admin1")
	st.SetTOTP(ctx, u.ID, "SEGREDO", true, "[]")
	st.UpdateUser(ctx, u.ID, "", "", "admin", true) // desativado
	st.CreateSession(ctx, auth.SHA256Hex("tok"), u.ID, false, time.Now().Add(time.Hour), "1.1.1.1", "x")

	if err := ResetPassword(ctx, st, "naoexiste", "nova-senha-1234", false); err == nil {
		t.Fatal("usuário inexistente")
	}
	if err := ResetPassword(ctx, st, "admin1", "curta", false); err == nil {
		t.Fatal("senha fraca")
	}
	if err := ResetPassword(ctx, st, "ADMIN1", "nova-senha-1234", true); err != nil {
		t.Fatal(err)
	}
	u, _ = st.UserByUsername(ctx, "admin1")
	if !auth.CheckPassword("nova-senha-1234", u.PasswordHash) || auth.CheckPassword("senha-antiga-123", u.PasswordHash) {
		t.Fatal("senha não trocada")
	}
	if u.TOTPEnabled || u.Disabled {
		t.Fatalf("o 2FA deveria ser desativado e a conta reativada: %+v", u)
	}
	if _, err := st.GetSession(ctx, auth.SHA256Hex("tok")); err == nil {
		t.Fatal("as sessões antigas deveriam ser encerradas")
	}
	if a, _ := st.ListAudit(ctx, 10); len(a) < 2 || a[0].Action != "cli.reset_password" {
		t.Fatalf("auditoria: %+v", a)
	}
}
