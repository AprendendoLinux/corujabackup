// Package cloud define os conectores de nuvem do Coruja Backup. Cada provedor implementa Provider; as credenciais
// ficam só no Server. O arquivo de backup vai do agente direto para a nuvem por uma sessão de upload emitida aqui.
package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	TypeOneDrive = "onedrive"
	TypeGDrive   = "gdrive"
)

type Quota struct{ Total, Used int64 } // Total 0 = desconhecido/ilimitado

type RemoteFile struct {
	ID       string
	Name     string
	Size     int64
	QuickXor string // base64 (OneDrive)
	SHA1     string // hex (OneDrive pessoal)
	MD5      string // hex (Google Drive)
	// ContentHash é o hash próprio do Dropbox (hex).
	ContentHash string
	Modified    time.Time
}

// UploadSession é uma sessão de upload retomável já autenticada (o agente não recebe nenhum token).
type UploadSession struct {
	Protocol  string // "graph" | "gdrive" | "dropbox"
	Token     string // só no Dropbox: token de curta duração para o envio
	Path      string // só no Dropbox: caminho de destino
	URL       string
	ChunkSize int64
	ExpiresAt time.Time
}

type Provider interface {
	Type() string
	// Account devolve um rótulo legível da conta conectada (e-mail/nome).
	Account(ctx context.Context) (string, error)
	Quota(ctx context.Context) (Quota, error)
	// NewUpload cria a pasta (se preciso) e abre uma sessão de upload do arquivo name (size bytes).
	NewUpload(ctx context.Context, folder, name string, size int64) (*UploadSession, error)
	// Stat devolve o arquivo (com os hashes que a nuvem calculou); ErrNotFound se não existir.
	Stat(ctx context.Context, folder, name string) (*RemoteFile, error)
	List(ctx context.Context, folder string) ([]RemoteFile, error)
	// Delete remove definitivamente quando o provedor permite (sem passar pela lixeira, que consome cota).
	Delete(ctx context.Context, folder string, f RemoteFile) error
	// Open baixa o arquivo (para restauração/download). O chamador fecha o corpo.
	Open(ctx context.Context, folder, name string) (io.ReadCloser, int64, error)
}

var (
	ErrNotFound = errors.New("não encontrado na nuvem")
	ErrAuth     = errors.New("a nuvem recusou as credenciais (reconecte a conta)")
)

// APIError é um erro HTTP da API do provedor (o corpo é cortado e nunca contém tokens).
type APIError struct {
	Status int
	Body   string
	Op     string
}

func (e *APIError) Error() string {
	b := strings.TrimSpace(e.Body)
	if len(b) > 300 {
		b = b[:300] + "…"
	}
	return fmt.Sprintf("%s: HTTP %d %s", e.Op, e.Status, b)
}

func (e *APIError) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	case ErrAuth:
		return e.Status == http.StatusUnauthorized
	}
	return false
}

// CleanFolder normaliza e valida a pasta remota ("A/B/C"): sem barras nas pontas, sem "." nem "..", sem vazios.
func CleanFolder(f string) (string, error) {
	f = strings.Trim(strings.TrimSpace(f), "/")
	if f == "" {
		return "", errors.New("pasta da nuvem vazia")
	}
	segs := strings.Split(f, "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "\\:*?\"<>|\x00") || len(s) > 200 {
			return "", fmt.Errorf("pasta da nuvem inválida: %q", f)
		}
	}
	return strings.Join(segs, "/"), nil
}

// escPath escapa cada segmento de caminho para uso em URL.
func escPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// Retention decide quais arquivos remover da nuvem. Considera só nomes <prefixo>-AAAAMMDD-HHMMSS.tar.gz; mantém sempre
// pelo menos o mais novo. Remove o que passar de keep (se >0) E/OU for mais velho que keepDays (se >0).
func Retention(files []RemoteFile, prefix string, keep, keepDays int, now time.Time) []RemoteFile {
	var mine []RemoteFile
	for _, f := range files {
		if _, ok := parseBackupName(f.Name, prefix); ok {
			mine = append(mine, f)
		}
	}
	// ordem cronológica pelo carimbo no nome (mais novo primeiro)
	for i := 1; i < len(mine); i++ {
		for j := i; j > 0 && mine[j].Name > mine[j-1].Name; j-- {
			mine[j], mine[j-1] = mine[j-1], mine[j]
		}
	}
	var del []RemoteFile
	for i, f := range mine {
		if i == 0 {
			continue // o mais novo nunca é removido
		}
		old := false
		if keep > 0 && i >= keep {
			old = true
		}
		if keepDays > 0 {
			if ts, ok := parseBackupName(f.Name, prefix); ok && now.Sub(ts) > time.Duration(keepDays)*24*time.Hour {
				old = true
			}
		}
		if old {
			del = append(del, f)
		}
	}
	return del
}

func parseBackupName(name, prefix string) (time.Time, bool) {
	if !strings.HasPrefix(name, prefix+"-") || !strings.HasSuffix(name, ".tar.gz") {
		return time.Time{}, false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, prefix+"-"), ".tar.gz")
	t, err := time.Parse("20060102-150405", stamp)
	return t, err == nil
}
