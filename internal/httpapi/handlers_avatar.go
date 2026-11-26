package httpapi

// Foto (avatar) do usuário: cada um troca a sua; qualquer usuário autenticado pode ver (aparece no menu e em Usuários).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"

	"github.com/AprendendoLinux/corujabackup/internal/store"
	"github.com/AprendendoLinux/corujabackup/internal/themepkg"
)

const maxAvatarBytes = 2 << 20 // a foto enviada; a guardada é reduzida para 256×256 em JPEG

func (s *Server) uploadAvatar(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAvatarBytes+1024)
	b, err := io.ReadAll(r.Body)
	if err != nil || len(b) == 0 || len(b) > maxAvatarBytes {
		writeErr(w, 400, "a foto deve ter até 2 MB")
		return
	}
	if typ, ok := themepkg.SniffImage(b); !ok || typ == "image/svg+xml" { // só imagens raster: SVG não é aceito como foto
		writeErr(w, 400, "use uma imagem PNG, JPEG, GIF ou WebP")
		return
	}
	b, err = normalizeAvatar(b)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	typ := "image/jpeg"
	sum := sha256.Sum256(b)
	ver := hex.EncodeToString(sum[:6])
	if err := s.Store.SetAvatar(r.Context(), cu.User.ID, b, typ, ver); err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "user.avatar_set", typ, s.clientIP(r))
	writeJSON(w, 200, map[string]string{"avatar_version": ver})
}

func (s *Server) deleteAvatar(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	if err := s.Store.SetAvatar(r.Context(), cu.User.ID, nil, "", ""); err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "user.avatar_removed", "", s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) avatar(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	b, typ, err := s.Store.Avatar(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "sem foto")
		return
	}
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	h := w.Header()
	h.Set("Content-Type", typ)
	h.Set("Cache-Control", "private, max-age=3600") // o endereço muda com a versão da foto
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}
