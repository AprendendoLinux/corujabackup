package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/store"
)

// streamRun envia, por SSE (Server-Sent Events), o log e o andamento de uma execução ao vivo. Termina sozinho quando a
// execução acaba e todo o log foi entregue. Cada conexão consulta o banco 1x por segundo (poucas conexões simultâneas).
func (s *Server) streamRun(w http.ResponseWriter, r *http.Request, cu *ctxUser) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "id inválido")
		return
	}
	// limites de transmissões simultâneas (cada uma segura uma conexão e consulta o banco 1x/s)
	if s.sseTotal.Add(1) > 60 {
		s.sseTotal.Add(-1)
		writeErr(w, 429, "transmissões ao vivo demais no momento; tente em instantes")
		return
	}
	defer s.sseTotal.Add(-1)
	uk := cu.User.ID
	cnt, _ := s.sseUser.LoadOrStore(uk, new(atomic.Int32))
	uc := cnt.(*atomic.Int32)
	if uc.Add(1) > 8 {
		uc.Add(-1)
		writeErr(w, 429, "você já tem transmissões ao vivo demais abertas")
		return
	}
	defer uc.Add(-1)
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "transmissão não suportada")
		return
	}
	run, err := s.Store.GetRun(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "execução não encontrada")
		return
	}
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		fl.Flush()
	}
	lastSig, quiet, ping := "", 0, 0
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		logs, _ := s.Store.RunLogsAfter(r.Context(), id, after, 500)
		for _, l := range logs {
			send("log", l)
			after = l.Seq
		}
		if cur, err := s.Store.GetRun(r.Context(), id); err == nil {
			run = cur
		}
		sig := fmt.Sprintf("%s|%s|%d|%d|%s|%s|%d", run.Status, run.Phase, run.ProgressPct, run.Files, run.CloudStatus, run.Error, run.BytesOutput)
		if sig != lastSig {
			lastSig = sig
			send("run", run)
		}
		if store.IsFinal(run.Status) && len(logs) == 0 {
			if quiet++; quiet >= 3 { // dá tempo ao log final (enviado junto do relatório) de chegar
				send("end", map[string]string{"status": run.Status})
				return
			}
		} else {
			quiet = 0
		}
		if ping++; ping%15 == 0 {
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
		select {
		case <-tick.C:
		case <-r.Context().Done():
			return
		}
	}
}
