// Package dockerapi é um cliente mínimo da Docker Engine API pelo socket Unix (sem depender do binário "docker").
// Cobre só o que o Coruja Backup precisa: listar/inspecionar contêineres, executar comandos dentro deles (com
// entrada/saída por streaming) e criar/remover contêineres descartáveis para o teste de restauração.
package dockerapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const DefaultSocket = "/var/run/docker.sock"

type Client struct {
	Socket string
	HTTP   *http.Client
}

func New(socket string) *Client {
	if socket == "" {
		socket = DefaultSocket
	}
	c := &Client{Socket: socket}
	c.HTTP = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		},
	}}
	return c
}

// Available diz se o daemon responde no socket.
func (c *Client) Available(ctx context.Context) bool {
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://docker/_ping", nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("docker: HTTP %d: %s", e.Status, e.Message) }

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("não foi possível falar com o Docker (%s): %w", c.Socket, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		var m struct{ Message string }
		_ = json.Unmarshal(b, &m)
		if m.Message == "" {
			m.Message = strings.TrimSpace(string(b))
		}
		return &APIError{Status: resp.StatusCode, Message: m.Message}
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

type Container struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}

func (c Container) Name() string {
	if len(c.Names) == 0 {
		return c.ID[:12]
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

func (c *Client) List(ctx context.Context) ([]Container, error) {
	var out []Container
	return out, c.do(ctx, "GET", "/containers/json", nil, &out)
}

type Inspect struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	State  struct{ Running bool }
	Config struct {
		Image  string
		Env    []string
		Labels map[string]string
	}
}

// Env devolve as variáveis de ambiente do contêiner como mapa.
func (i *Inspect) Env() map[string]string {
	m := map[string]string{}
	for _, kv := range i.Config.Env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func (c *Client) Inspect(ctx context.Context, nameOrID string) (*Inspect, error) {
	var out Inspect
	if err := c.do(ctx, "GET", "/containers/"+pathEsc(nameOrID)+"/json", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func pathEsc(s string) string {
	return strings.NewReplacer("/", "", "?", "", "#", "", " ", "").Replace(s)
}

// ---------------------------------------------------------------- exec

type ExecOptions struct {
	Cmd    []string
	Env    []string // "CHAVE=valor" (vai no corpo da API, não aparece na lista de processos do host)
	User   string
	Stdin  io.Reader // opcional; é fechado (meia-conexão) ao terminar
	Stdout io.Writer
	Stderr io.Writer
}

// Exec roda o comando dentro do contêiner e devolve o código de saída.
func (c *Client) Exec(ctx context.Context, container string, o ExecOptions) (int, error) {
	var created struct {
		ID string `json:"Id"`
	}
	body := map[string]any{"AttachStdin": o.Stdin != nil, "AttachStdout": true, "AttachStderr": true, "Tty": false, "Cmd": o.Cmd, "Env": o.Env}
	if o.User != "" {
		body["User"] = o.User
	}
	if err := c.do(ctx, "POST", "/containers/"+pathEsc(container)+"/exec", body, &created); err != nil {
		return -1, err
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return -1, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	payload := []byte(`{"Detach":false,"Tty":false}`)
	req := fmt.Sprintf("POST /exec/%s/start HTTP/1.1\r\nHost: docker\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n", created.ID, len(payload))
	if _, err := conn.Write(append([]byte(req), payload...)); err != nil {
		return -1, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return -1, err
	}
	if resp.StatusCode != 101 && resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return -1, &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(b))}
	}
	if o.Stdin != nil {
		go func() {
			_, _ = io.Copy(conn, o.Stdin)
			if uc, ok := conn.(*net.UnixConn); ok {
				_ = uc.CloseWrite()
			}
		}()
	}
	if err := demux(br, o.Stdout, o.Stderr); err != nil && ctx.Err() == nil {
		return -1, err
	}
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	var ins struct {
		ExitCode int
		Running  bool
	}
	if err := c.do(ctx, "GET", "/exec/"+created.ID+"/json", nil, &ins); err != nil {
		return -1, err
	}
	return ins.ExitCode, nil
}

// demux separa o fluxo multiplexado do Docker (cabeçalho de 8 bytes: tipo, 3 zeros, tamanho big-endian).
func demux(r io.Reader, stdout, stderr io.Writer) error {
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		n := int64(binary.BigEndian.Uint32(hdr[4:]))
		var w io.Writer = io.Discard
		switch hdr[0] {
		case 1:
			if stdout != nil {
				w = stdout
			}
		case 2:
			if stderr != nil {
				w = stderr
			}
		}
		if _, err := io.CopyN(w, r, n); err != nil {
			return err
		}
	}
}

// ---------------------------------------------------------------- contêineres descartáveis

type CreateOptions struct {
	Image  string
	Env    []string
	Cmd    []string
	Memory int64 // bytes (0 = sem limite)
	Labels map[string]string
}

func (c *Client) Create(ctx context.Context, o CreateOptions) (string, error) {
	body := map[string]any{"Image": o.Image, "Env": o.Env, "Labels": o.Labels,
		"HostConfig": map[string]any{"Memory": o.Memory, "AutoRemove": false, "NetworkMode": "none"}}
	if len(o.Cmd) > 0 {
		body["Cmd"] = o.Cmd
	}
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.do(ctx, "POST", "/containers/create", body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (c *Client) Start(ctx context.Context, id string) error {
	return c.do(ctx, "POST", "/containers/"+id+"/start", nil, nil)
}

// Remove apaga o contêiner (e seus volumes anônimos), mesmo em execução.
func (c *Client) Remove(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/containers/"+id+"?force=true&v=true", nil, nil)
}

// Pull baixa a imagem se ainda não existir localmente.
func (c *Client) Pull(ctx context.Context, image string) error {
	if err := c.do(ctx, "GET", "/images/"+pathEsc(image)+"/json", nil, nil); err == nil {
		return nil
	}
	name, tag := image, "latest"
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		name, tag = image[:i], image[i+1:]
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://docker/images/create?fromImage="+name+"&tag="+tag, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body) // o corpo só termina quando o download acaba
	if resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(b))}
	}
	if bytes.Contains(b, []byte(`"error"`)) {
		return fmt.Errorf("falha ao baixar a imagem %s: %s", image, strings.TrimSpace(string(b)))
	}
	return nil
}
