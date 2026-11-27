package httpapi

// "Clicar e selecionar": navegação pelas pastas do agente e do destino de um job, e o endereço público do canal dos agentes.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/agentcore"
	"github.com/AprendendoLinux/corujabackup/internal/cloud"
	"github.com/AprendendoLinux/corujabackup/internal/netdest"
)

// agentBrowse lista uma pasta do agente: area=host (o que salvar), area=disk (escolher a pasta do backup local, com o
// espaço disponível) ou area=backups (subpastas da pasta de backups padrão).
func (s *Server) agentBrowse(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	area, p := r.URL.Query().Get("area"), r.URL.Query().Get("path")
	if area != agentcore.BrowseHost && area != agentcore.BrowseBackups && area != agentcore.BrowseDiskArea {
		writeErr(w, 400, "área de navegação inválida")
		return
	}
	if len(p) > 1024 || strings.ContainsRune(p, 0) {
		writeErr(w, 400, "caminho inválido")
		return
	}
	payload, _ := json.Marshal(agentcore.BrowseRequest{Area: area, Path: p})
	s.agentCall(w, r, id, "fs_list", payload, 30*time.Second)
}

// destFolders lista as subpastas de um destino. Nas nuvens quem consulta é o Server; nos destinos de rede, o agente do job.
func (s *Server) destFolders(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	id, ok := pathID(r)
	if !ok || s.Clouds == nil {
		writeErr(w, 400, "id inválido")
		return
	}
	p := strings.Trim(r.URL.Query().Get("path"), "/")
	if p != "" {
		clean, err := cloud.CleanFolder(p)
		if err != nil || len(p) > 1024 {
			writeErr(w, 400, "pasta inválida")
			return
		}
		p = clean
	}
	cfg, err := s.Clouds.DestConfig(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "destino não encontrado")
		return
	}
	if cfg != nil {
		agentID, err := strconv.ParseInt(r.URL.Query().Get("agent_id"), 10, 64)
		if err != nil || agentID < 1 {
			writeErr(w, 400, "escolha o agente do job para navegar neste destino")
			return
		}
		if msg := s.destAgentCheckID(r.Context(), cfg, agentID); msg != "" {
			writeErr(w, 422, msg)
			return
		}
		payload, _ := json.Marshal(struct {
			Config netdest.Config `json:"config"`
			Path   string         `json:"path"`
		}{*cfg, p})
		s.agentCall(w, r, agentID, "dest_folders", payload, 60*time.Second)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	list, err := s.Clouds.Folders(ctx, id, p)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("a nuvem demorou demais para responder")
		}
		writeErr(w, 422, err.Error())
		return
	}
	// só nomes que podem de fato ser usados como pasta de um job
	out := []string{}
	for _, n := range list {
		if clean, err := cloud.CleanFolder(n); err == nil && clean == n && !strings.Contains(n, "/") {
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	writeJSON(w, 200, map[string]any{"folders": slices.Compact(out)})
}

// parseAgentAddress valida o endereço "host:porta" onde o Server conecta no agente.
func parseAgentAddress(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", true
	}
	host, port, err := net.SplitHostPort(v)
	if err != nil || !agentHostRE.MatchString(host) {
		return "", false
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", false
	}
	return net.JoinHostPort(host, strconv.Itoa(n)), true
}

// agentDirSize pede ao agente o tamanho de uma pasta da máquina (seletor do que salvar).
func (s *Server) agentDirSize(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	p := r.URL.Query().Get("path")
	if p == "" || len(p) > 1024 || strings.ContainsRune(p, 0) {
		writeErr(w, 400, "caminho inválido")
		return
	}
	payload, _ := json.Marshal(agentcore.SizeRequest{Path: p})
	s.agentCall(w, r, id, "fs_size", payload, 45*time.Second)
}
