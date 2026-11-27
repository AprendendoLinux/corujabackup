package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/agentcore"
	"github.com/AprendendoLinux/corujabackup/internal/agentsrv"
	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/dbdump"
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
		Dest     string   `json:"dest"` // pasta do servidor escolhida na navegação (absoluta)
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
	in.Target, in.Dest = strings.TrimSpace(in.Target), strings.TrimSpace(in.Dest)
	if in.Dest != "" {
		if in.Original {
			writeErr(w, 400, "escolha um destino só: a pasta do servidor ou o local original")
			return
		}
		if cu.User.Role != "admin" {
			writeErr(w, 403, "só administradores restauram numa pasta do servidor (pode substituir arquivos que já estejam lá)")
			return
		}
		clean, err := agentcore.CheckLocalDir(in.Dest)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		in.Dest = clean
		if a, err := s.Store.AgentByID(r.Context(), run.AgentID); err == nil && a.AgentVersion != "" && !versionAtLeast(a.AgentVersion, restoreDestAgent) {
			writeErr(w, 422, "o agente "+a.Name+" (versão "+a.AgentVersion+") não restaura numa pasta escolhida: atualize-o para a versão 0.0.27 ou mais nova")
			return
		}
		in.Target = ""
	} else if !in.Original {
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
		Chain: chain, ChainSha256: shas, Paths: in.Paths, Original: in.Original, Target: in.Target, DestDir: in.Dest}
	if s.actionErr(w, s.Agents.Action(run.AgentID, act)) {
		return
	}
	where := "pasta " + in.Target
	switch {
	case in.Original:
		where = "LOCAL ORIGINAL"
	case in.Dest != "":
		where = "pasta do servidor " + in.Dest
	}
	sample := strings.Join(in.Paths[:min(len(in.Paths), 5)], ", ")
	if len(in.Paths) > 5 {
		sample += fmt.Sprintf(" e mais %d", len(in.Paths)-5)
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "run.restore_files", fmt.Sprintf("%s / %s -> %s: %s", run.JobName, run.OutputName, where, sample), s.clientIP(r))
	writeJSON(w, 202, map[string]any{"target": in.Target, "original": in.Original, "dest": in.Dest})
}

// ---------------------------------------------------------------- download para o navegador

var downloadAgent = [3]int{0, 0, 24}

// restoreDestAgent: a restauração numa pasta escolhida do servidor chegou na 0.0.27 do agente.
var restoreDestAgent = [3]int{0, 0, 27}

type readyDownload struct {
	rc     io.ReadCloser
	name   string
	userID int64
	exp    time.Time
}

var (
	dlMu     sync.Mutex
	dlTokens = map[string]*readyDownload{}
)

// purgeDownloads fecha os downloads preparados e não buscados (o agente desiste do envio).
func purgeDownloads(now time.Time) {
	dlMu.Lock()
	defer dlMu.Unlock()
	for k, d := range dlTokens {
		if now.After(d.exp) {
			d.rc.Close()
			delete(dlTokens, k)
		}
	}
}

// prepareDownload pede ao agente os itens escolhidos (um arquivo como está ou um .zip) e espera o início do envio;
// devolve o endereço, de uso único, que o navegador abre para baixar.
func (s *Server) prepareDownload(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	run, job, ok := s.runForAction(w, r)
	if !ok || !filesRun(w, job) {
		return
	}
	if a, err := s.Store.AgentByID(r.Context(), run.AgentID); err == nil && a.AgentVersion != "" && !versionAtLeast(a.AgentVersion, downloadAgent) {
		writeErr(w, 422, "o agente "+a.Name+" (versão "+a.AgentVersion+") não envia downloads ao navegador: atualize-o para a versão 0.0.24 ou mais nova")
		return
	}
	var in struct {
		Paths []string `json:"paths"`
		Zip   bool     `json:"zip"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Paths) == 0 || len(in.Paths) > 5000 {
		writeErr(w, 400, "escolha o que baixar (até 5000 itens)")
		return
	}
	for _, p := range in.Paths {
		if !strings.HasPrefix(p, "/") || len(p) > 4096 || strings.ContainsRune(p, 0) {
			writeErr(w, 400, "caminho inválido: "+p)
			return
		}
	}
	chain, shas, msg := s.chainFor(r, job, run)
	if msg != "" {
		writeErr(w, 409, msg)
		return
	}
	act := &pb.AgentAction{RunUid: run.UID, JobId: job.ID, FileName: run.OutputName, Subdir: job.DestSubdir, Sha256: run.SHA256,
		Chain: chain, ChainSha256: shas, Paths: in.Paths, Zip: in.Zip}
	// o pedido do navegador pode acabar antes do download: o fluxo vive no contexto do Server, até ser buscado ou expirar
	rc, name, err := s.Agents.FetchDownload(context.WithoutCancel(r.Context()), run.AgentID, act, 90*time.Second)
	if err != nil {
		if errors.Is(err, agentsrv.ErrOffline) {
			writeErr(w, 409, "o agente está offline")
			return
		}
		writeErr(w, 422, err.Error())
		return
	}
	purgeDownloads(time.Now())
	tok := auth.NewUUID()
	dlMu.Lock()
	dlTokens[tok] = &readyDownload{rc: rc, name: name, userID: cu.User.ID, exp: time.Now().Add(2 * time.Minute)}
	dlMu.Unlock()
	sample := strings.Join(in.Paths[:min(len(in.Paths), 5)], ", ")
	if len(in.Paths) > 5 {
		sample += fmt.Sprintf(" e mais %d", len(in.Paths)-5)
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "run.download_files", fmt.Sprintf("%s / %s: %s", run.JobName, run.OutputName, sample), s.clientIP(r))
	writeJSON(w, 200, map[string]string{"url": "/api/downloads/" + tok, "name": name})
}

// serveDownload entrega o download preparado (uma vez, só a quem pediu).
func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	tok := r.PathValue("token")
	dlMu.Lock()
	d := dlTokens[tok]
	if d != nil && d.userID == cu.User.ID {
		delete(dlTokens, tok)
	} else {
		d = nil
	}
	dlMu.Unlock()
	if d == nil || time.Now().After(d.exp) {
		if d != nil {
			d.rc.Close()
		}
		writeErr(w, 404, "download expirado ou já baixado: peça de novo")
		return
	}
	defer d.rc.Close()
	h := w.Header()
	ct := "application/octet-stream"
	if strings.HasSuffix(d.name, ".zip") {
		ct = "application/zip"
	}
	h.Set("Content-Type", ct)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.name}))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, d.rc)
}

// ---------------------------------------------------------------- restauração de bancos (0.0.24)

func dbRun(w http.ResponseWriter, job *store.Job) bool {
	if job.Kind != "database" {
		writeErr(w, 409, "esta execução não é de um job de banco de dados")
		return false
	}
	return true
}

// runDatabases lista os bancos contidos num backup de banco.
func (s *Server) runDatabases(w http.ResponseWriter, r *http.Request, _ *ctxUser) {
	run, job, ok := s.runForAction(w, r)
	if !ok || !dbRun(w, job) {
		return
	}
	if a, err := s.Store.AgentByID(r.Context(), run.AgentID); err == nil && a.AgentVersion != "" && !versionAtLeast(a.AgentVersion, downloadAgent) {
		writeErr(w, 422, "o agente "+a.Name+" (versão "+a.AgentVersion+") não restaura bancos pela interface: atualize-o para a versão 0.0.24 ou mais nova")
		return
	}
	payload, _ := json.Marshal(map[string]any{"job_id": job.ID, "archive": run.OutputName, "subdir": job.DestSubdir, "sha256": run.SHA256})
	s.agentCall(w, r, run.AgentID, "db_contents", payload, 5*time.Minute)
}

// restoreDatabases restaura bancos de um backup no servidor do job: num banco novo (operador) ou sobrescrevendo um
// existente (só administrador).
func (s *Server) restoreDatabases(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	run, job, ok := s.runForAction(w, r)
	if !ok || !dbRun(w, job) {
		return
	}
	if a, err := s.Store.AgentByID(r.Context(), run.AgentID); err == nil && a.AgentVersion != "" && !versionAtLeast(a.AgentVersion, downloadAgent) {
		writeErr(w, 422, "o agente "+a.Name+" (versão "+a.AgentVersion+") não restaura bancos pela interface: atualize-o para a versão 0.0.24 ou mais nova")
		return
	}
	var in struct {
		Databases []struct {
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"databases"`
		Overwrite bool `json:"overwrite"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Databases) == 0 || len(in.Databases) > 200 {
		writeErr(w, 400, "escolha os bancos a restaurar")
		return
	}
	if in.Overwrite && cu.User.Role != "admin" {
		writeErr(w, 403, "só administradores sobrescrevem bancos existentes")
		return
	}
	act := &pb.AgentAction{Kind: "restore_db", RunUid: run.UID, JobId: job.ID, FileName: run.OutputName, Subdir: job.DestSubdir, Sha256: run.SHA256, Original: in.Overwrite}
	seen := map[string]bool{}
	var desc []string
	for _, d := range in.Databases {
		d.Source, d.Target = strings.TrimSpace(d.Source), strings.TrimSpace(d.Target)
		if !dbdump.ValidName(d.Source) || !dbdump.ValidName(d.Target) {
			writeErr(w, 400, "nome de banco inválido: use letras, números, ponto, hífen e sublinhado")
			return
		}
		if d.Source == d.Target && !in.Overwrite {
			writeErr(w, 400, "restaurar com o mesmo nome do original sobrescreve o banco: escolha outro nome ou marque sobrescrever")
			return
		}
		if seen[d.Target] {
			writeErr(w, 400, "dois bancos com o mesmo destino: "+d.Target)
			return
		}
		seen[d.Target] = true
		act.Paths, act.Targets = append(act.Paths, d.Source), append(act.Targets, d.Target)
		desc = append(desc, d.Source+" -> "+d.Target)
	}
	if s.actionErr(w, s.Agents.Action(run.AgentID, act)) {
		return
	}
	mode := "banco novo"
	if in.Overwrite {
		mode = "SOBRESCREVENDO"
	}
	s.Store.Audit(r.Context(), cu.User.ID, cu.User.Username, "run.restore_db", fmt.Sprintf("%s / %s (%s): %s", run.JobName, run.OutputName, mode, strings.Join(desc, ", ")), s.clientIP(r))
	writeJSON(w, 202, map[string]bool{"ok": true})
}
