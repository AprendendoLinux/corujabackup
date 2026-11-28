// Package adminops reúne operações de administração feitas pela linha de comando do Server (recuperação de acesso).
package adminops

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

var userRE = regexp.MustCompile(`^[a-z0-9._-]{3,64}$`)

// ResetPassword define uma nova senha para o usuário, encerra todas as sessões dele e, se pedido, desativa o 2FA
// (recuperação quando o administrador perdeu a senha ou o autenticador).
func ResetPassword(ctx context.Context, st *store.Store, username, newPassword string, disable2FA bool) error {
	username = strings.ToLower(strings.TrimSpace(username))
	u, err := st.UserByUsername(ctx, username)
	if err != nil {
		return fmt.Errorf("usuário %q não encontrado", username)
	}
	if err := auth.ValidatePassword(newPassword); err != nil {
		return err
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := st.SetPassword(ctx, u.ID, hash); err != nil {
		return err
	}
	if disable2FA {
		if err := st.SetTOTP(ctx, u.ID, "", false, ""); err != nil {
			return err
		}
	}
	if u.Disabled {
		if err := st.UpdateUser(ctx, u.ID, u.FirstName, u.LastName, u.Role, false); err != nil {
			return err
		}
	}
	_ = st.DeleteUserSessions(ctx, u.ID, "")
	st.Audit(ctx, u.ID, u.Username, "cli.reset_password", fmt.Sprintf("2fa desativado=%v", disable2FA), "cli")
	return nil
}

// CreateAdmin cria um administrador (para o caso de nenhum acesso restar).
func CreateAdmin(ctx context.Context, st *store.Store, username, password string) error {
	username = strings.ToLower(strings.TrimSpace(username))
	if !userRE.MatchString(username) {
		return errors.New("usuário inválido: 3 a 64 caracteres (letras minúsculas, números, ponto, hífen ou sublinhado)")
	}
	if err := auth.ValidatePassword(password); err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	id, err := st.CreateUser(ctx, username, username, "", hash, "admin")
	if errors.Is(err, store.ErrDuplicate) {
		return fmt.Errorf("o usuário %q já existe (use reset-password)", username)
	}
	if err != nil {
		return err
	}
	st.Audit(ctx, id, username, "cli.create_admin", "", "cli")
	return nil
}

// Usuário e senha padrão da primeira subida.
const (
	DefaultAdminUser     = "admin"
	DefaultAdminPassword = "admin"
)

// EnsureDefaultAdmin cria o administrador padrão (admin/admin) quando o banco ainda não tem nenhum usuário. Ele é
// obrigado a trocar a senha no primeiro login: até lá, a API só aceita a troca de senha. created=false se já havia
// usuários.
func EnsureDefaultAdmin(ctx context.Context, st *store.Store) (created bool, err error) {
	n, err := st.CountUsers(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	hash, err := auth.HashPassword(DefaultAdminPassword)
	if err != nil {
		return false, err
	}
	if _, err := st.CreateDefaultAdmin(ctx, DefaultAdminUser, hash); err != nil {
		if errors.Is(err, store.ErrDuplicate) { // outro processo criou ao mesmo tempo
			return false, nil
		}
		return false, err
	}
	return true, nil
}
