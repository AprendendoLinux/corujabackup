package cloud

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Provedor que ignora o Range (responde 200 com o arquivo inteiro): o trecho é cortado do lado de cá.
func TestTrechoComProvedorQueIgnoraRange(t *testing.T) {
	data := "0123456789abcdefghij"
	for _, honor := range []bool{true, false} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if honor {
				http.ServeContent(w, r, "", zeroTime, stringsReader(data))
				return
			}
			_, _ = io.WriteString(w, data)
		}))
		req, _ := http.NewRequest("GET", srv.URL, nil)
		setRange(req, 5, 7)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		rc, n, err := fitRange(resp, 5, 7)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		srv.Close()
		if string(b) != "56789ab" || n != 7 {
			t.Fatalf("respeita Range=%v: %q (%d)", honor, b, n)
		}
	}
}

var zeroTime = time.Time{}

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }
