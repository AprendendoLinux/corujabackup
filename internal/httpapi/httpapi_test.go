package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakePing struct{ err error }

func (f fakePing) PingContext(context.Context) error { return f.err }

func get(t *testing.T, s *Server, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec, m
}

func TestHealthOk(t *testing.T) {
	rec, m := get(t, &Server{Pinger: fakePing{}, Started: time.Now()}, "/api/health")
	if rec.Code != 200 || m["status"] != "ok" || m["database"] != true {
		t.Fatalf("%d %v", rec.Code, m)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("faltam cabeçalhos de segurança")
	}
}

func TestHealthBancoCaido(t *testing.T) {
	rec, m := get(t, &Server{Pinger: fakePing{errors.New("x")}, Started: time.Now()}, "/api/health")
	if rec.Code != 503 || m["status"] != "degraded" {
		t.Fatalf("%d %v", rec.Code, m)
	}
}

func TestVersaoERotaInexistente(t *testing.T) {
	s := &Server{Started: time.Now()}
	if _, m := get(t, s, "/api/version"); m["version"] == "" || m["name"] != "Coruja Backup" {
		t.Fatalf("%v", m)
	}
	if rec, _ := get(t, s, "/api/nada"); rec.Code != 404 {
		t.Fatalf("esperava 404, veio %d", rec.Code)
	}
}

func TestMetodoNaoPermitido(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Server{Started: time.Now()}).Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/health", nil))
	if rec.Code == 200 {
		t.Fatal("POST em /api/health não deveria funcionar")
	}
}
