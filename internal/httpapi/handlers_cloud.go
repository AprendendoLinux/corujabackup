package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/cloudmgr"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

var cloudNameRE = regexp.MustCompile(`^[\p{L}\p{N}._(][\p{L}\p{N}._ ()&,'-]{1,99}$`)

type cloudView struct {
	store.CloudAccount
	Jobs int `json:"jobs"`
}

func (s *Server) listClouds(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	accs, err := s.Store.ListClouds(r.Context())
	if err != nil {
		writeErr(w, 500, "erro interno")
		return
	}
	out := make([]cloudView, 0, len(accs))
	for _, a := range accs {
		n, _ := s.Store.JobsUsingCloud(r.Context(), a.ID)
		out = append(out, cloudView{a, n})
	}
	writeJSON(w, 200, map[string]any{"accounts": out, "defaults": map[string]any{"gdrive": cloud.HasDefaultApp(cloud.TypeGDrive), "gdrive_web": cloud.HasWebApp(cloud.TypeGDrive), "onedrive_web": cloud.HasWebApp(cloud.TypeOneDrive), "broker": strings.TrimRight(cloud.OAuthBrokerURL, "/")}})
}

func deviceView(d *cloud.Device) map[string]any {
	if d == nil {
		return nil
	}
	return map[string]any{"user_code": d.UserCode, "verification_uri": d.VerificationURI, "expires_at": d.ExpiresAt, "interval": d.Interval}
}

func (s *Server) createCloud(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	if s.Clouds == nil {
		writeErr(w, 503, "nuvem indisponível")
		return
	}
	var in struct {
		Name         string `json:"name"`
		Type         string `json:"type"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Tenant       string `json:"tenant"`
		DriveID      string `json:"drive_id"`
		HardDelete   *bool  `json:"hard_delete"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !cloudNameRE.MatchString(in.Name) {
		writeErr(w, 400, "nome inválido: use 2 a 100 caracteres (letras, números, espaço, ponto, hífen ou sublinhado)")
		return
	}
	if in.Type != cloud.TypeOneDrive && in.Type != cloud.TypeGDrive {
		writeErr(w, 400, "tipo de nuvem inválido (onedrive ou gdrive)")
		return
	}
	hard := in.Type == cloud.TypeOneDrive // apagar de vez evita que a lixeira do OneDrive consuma cota
	if in.HardDelete != nil {
		hard = *in.HardDelete
	}
	cfg := cloudmgr.AccountConfig{ClientID: strings.TrimSpace(in.ClientID), ClientSecret: strings.TrimSpace(in.ClientSecret), Tenant: strings.TrimSpace(in.Tenant), DriveID: strings.TrimSpace(in.DriveID), HardDelete: hard}
	id, dev, err := s.Clouds.Create(r.Context(), in.Name, in.Type, cfg)
	if errors.Is(err, store.ErrDuplicate) {
		writeErr(w, 409, "já existe uma conta de nuvem com esse nome")
		return
	}
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "cloud.created", in.Name+" ("+in.Type+")", s.clientIP(r))
	writeJSON(w, 201, map[string]any{"id": id, "device": deviceView(dev)})
}

// pollCloud tenta concluir a autorização (a interface chama a cada poucos segundos até conectar).
func (s *Server) pollCloud(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok || s.Clouds == nil {
		writeErr(w, 400, "id inválido")
		return
	}
	status, dev, err := s.Clouds.PollDevice(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "conta não encontrada")
		return
	}
	if err != nil {
		writeJSON(w, 200, map[string]any{"status": "error", "error": err.Error(), "device": deviceView(dev)})
		return
	}
	if status == "connected" {
		s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "cloud.connected", itoa64(id), s.clientIP(r))
	}
	writeJSON(w, 200, map[string]any{"status": status, "device": deviceView(dev)})
}

func (s *Server) reauthCloud(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok || s.Clouds == nil {
		writeErr(w, 400, "id inválido")
		return
	}
	dev, err := s.Clouds.Reauthorize(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "conta não encontrada")
		return
	}
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"device": deviceView(dev)})
}

func (s *Server) testCloud(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	id, ok := pathID(r)
	if !ok || s.Clouds == nil {
		writeErr(w, 400, "id inválido")
		return
	}
	label, q, err := s.Clouds.Test(r.Context(), id)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "account": label, "quota_total": q.Total, "quota_used": q.Used})
}

func (s *Server) deleteCloud(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	a, err := s.Store.GetCloud(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "conta não encontrada")
		return
	}
	if n, _ := s.Store.JobsUsingCloud(r.Context(), id); n > 0 {
		writeErr(w, 409, "há jobs usando esta conta; troque o destino deles antes de excluí-la")
		return
	}
	_ = s.Store.DeleteCloud(r.Context(), id)
	if s.Clouds != nil {
		s.Clouds.Invalidate(id)
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "cloud.deleted", a.Name, s.clientIP(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func itoa64(i int64) string { return strconv.FormatInt(i, 10) }

// beginCloudWeb inicia o login por popup: devolve o endereço do provedor para o navegador abrir.
func (s *Server) beginCloudWeb(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	if s.Clouds == nil {
		writeErr(w, 503, "nuvem indisponível")
		return
	}
	var in struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Origin string `json:"origin"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !cloudNameRE.MatchString(in.Name) {
		writeErr(w, 400, "nome inválido: use 2 a 100 caracteres (letras, números, espaço, ponto, hífen ou sublinhado)")
		return
	}
	u, state, err := s.Clouds.BeginWeb(r.Context(), in.Name, in.Type, in.Origin)
	if errors.Is(err, store.ErrDuplicate) {
		writeErr(w, 409, "já existe uma conta de nuvem com esse nome")
		return
	}
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"url": u, "state": state})
}

// finishCloudWeb recebe o código entregue pela janela do popup e conclui a conexão.
func (s *Server) finishCloudWeb(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	if s.Clouds == nil {
		writeErr(w, 503, "nuvem indisponível")
		return
	}
	var in struct {
		State string `json:"state"`
		Code  string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	id, err := s.Clouds.FinishWeb(r.Context(), in.State, in.Code)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "cloud.connected", itoa64(id), s.clientIP(r))
	writeJSON(w, 201, map[string]any{"id": id})
}
