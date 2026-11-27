package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

// Restauração inteligente (0.0.23): navegar no catálogo de um backup, ver as versões de um arquivo e restaurar só o que
// for escolhido. Quem guarda os catálogos e extrai é o agente do job.

var restoreAgent = [3]int{0, 0, 23}

func (s *Server) restoreAgentOK(w http.ResponseWriter, r *http.Request, agentID int64) bool {
	if a, err := s.Store.AgentByID(r.Context(), agentID); err == nil && a.AgentVersion != "" && !versionAtLeast(a.AgentVersion, restoreAgent) {
		writeErr(w, 422, "o agente "+a.Name+" (versão "+a.AgentVersion+") não tem a restauração por seleção: atualize-o para a versão 0.0.23 ou mais nova")
		return false
	}
	return true
}

func filesRun(w http.ResponseWriter, job *store.Job) bool {
	if job.Kind != "files" {
		writeErr(w, 409, "a navegação e a restauração por seleção são para jobs de pastas e arquivos; use \"Extrair no agente\"")
		return false
	}
	return true
}

// browseRun lista uma pasta do backup (?dir=/srv).
func (s *Server) browseRun(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	run, job, ok := s.runForAction(w, r)
	if !ok || !filesRun(w, job) || !s.restoreAgentOK(w, r, run.AgentID) {
		return
	}
	dir := r.URL.Query().Get("dir")
	if len(dir) > 4096 || strings.ContainsRune(dir, 0) {
		writeErr(w, 400, "pasta inválida")
		return
	}
	payload, _ := json.Marshal(map[string]any{"job_id": job.ID, "archive": run.OutputName, "dir": dir})
	s.agentCall(w, r, run.AgentID, "catalog_dir", payload, 60*time.Second)
}

// indexRun pede ao agente que monte o catálogo de um backup antigo a partir do próprio arquivo.
func (s *Server) indexRun(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	run, job, ok := s.runForAction(w, r)
	if !ok || !filesRun(w, job) || !s.restoreAgentOK(w, r, run.AgentID) {
		return
	}
	err := s.Agents.Action(run.AgentID, &pb.AgentAction{Kind: "index", RunUid: run.UID, JobId: job.ID, FileName: run.OutputName, Subdir: job.DestSubdir, Sha256: run.SHA256})
	if s.actionErr(w, err) {
		return
	}
	writeJSON(w, 202, map[string]bool{"ok": true})
}

// jobVersions lista as versões de um arquivo nos backups do job (?path=/srv/a.txt).
func (s *Server) jobVersions(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	job, err := s.Store.GetJob(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "job não encontrado")
		return
	}
	if !filesRun(w, job) || !s.restoreAgentOK(w, r, job.AgentID) {
		return
	}
	p := r.URL.Query().Get("path")
	if !strings.HasPrefix(p, "/") || len(p) > 4096 || strings.ContainsRune(p, 0) {
		writeErr(w, 400, "caminho inválido")
		return
	}
	payload, _ := json.Marshal(map[string]any{"job_id": job.ID, "path": p})
	s.agentCall(w, r, job.AgentID, "catalog_versions", payload, 90*time.Second)
}

// restoreRun restaura os caminhos escolhidos de um backup: numa pasta de restauração (padrão) ou, só para
// administradores, no local original, sobrescrevendo.
func (s *Server) restoreRun(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	run, job, ok := s.runForAction(w, r)
	if !ok || !filesRun(w, job) || !s.restoreAgentOK(w, r, run.AgentID) {
		return
	}
	var in struct {
		Paths    []string `json:"paths"`
		Original bool     `json:"original"`
		Target   string   `json:"target"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Paths) == 0 || len(in.Paths) > 5000 {
		writeErr(w, 400, "escolha o que restaurar (até 5000 itens)")
		return
	}
	for _, p := range in.Paths {
		if !strings.HasPrefix(p, "/") || len(p) > 4096 || strings.ContainsRune(p, 0) {
			writeErr(w, 400, "caminho inválido: "+p)
			return
		}
	}
	if in.Original && cu.User.Role != "admin" {
		writeErr(w, 403, "só administradores restauram no local original (sobrescreve arquivos do servidor)")
		return
	}
	in.Target = strings.TrimSpace(in.Target)
	if !in.Original {
		if in.Target == "" {
			in.Target = fmt.Sprintf("run-%d-%s", run.ID, time.Now().UTC().Format("20060102-150405"))
		}
		if !extractTargetRE.MatchString(in.Target) || strings.Contains(in.Target, "..") {
			writeErr(w, 400, "nome de pasta inválido: use letras, números, ponto, hífen e sublinhado")
			return
		}
	} else {
		in.Target = ""
	}
	chain, shas, msg := s.chainFor(r, job, run)
	if msg != "" {
		writeErr(w, 409, msg)
		return
	}
	act := &pb.AgentAction{Kind: "restore_files", RunUid: run.UID, JobId: job.ID, FileName: run.OutputName, Subdir: job.DestSubdir, Sha256: run.SHA256,
		Chain: chain, ChainSha256: shas, Paths: in.Paths, Original: in.Original, Target: in.Target}
	if s.actionErr(w, s.Agents.Action(run.AgentID, act)) {
		return
	}
	where := "pasta " + in.Target
	if in.Original {
		where = "LOCAL ORIGINAL"
	}
	sample := strings.Join(in.Paths[:min(len(in.Paths), 5)], ", ")
	if len(in.Paths) > 5 {
		sample += fmt.Sprintf(" e mais %d", len(in.Paths)-5)
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "run.restore_files", fmt.Sprintf("%s / %s -> %s: %s", run.JobName, run.OutputName, where, sample), s.clientIP(r))
	writeJSON(w, 202, map[string]any{"target": in.Target, "original": in.Original})
}
