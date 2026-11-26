// Package dbdump faz dumps lógicos de MySQL/MariaDB, PostgreSQL e MongoDB, dentro de contêineres (pela Docker API) ou
// com as ferramentas de cliente do próprio agente (modo rede), e confere se o resultado é restaurável.
package dbdump

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/AprendendoLinux/corujabackup/internal/dockerapi"
)

// Cmd descreve um comando a executar. Segredos vão em Env (nunca na linha de comando).
type Cmd struct {
	Args   []string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
}

// Runner executa comandos onde o banco está (no contêiner ou no próprio agente).
type Runner interface {
	// Run devolve o código de saída e o final do stderr (até 8 KiB).
	Run(ctx context.Context, c Cmd) (exit int, stderr string, err error)
	Where() string
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// LocalRunner usa os clientes instalados no próprio agente (modo rede).
type LocalRunner struct{}

func (LocalRunner) Where() string { return "agente (modo rede)" }

func (LocalRunner) Run(ctx context.Context, c Cmd) (int, string, error) {
	cmd := exec.CommandContext(ctx, c.Args[0], c.Args[1:]...)
	cmd.Env = append([]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}, c.Env...)
	cmd.Stdin = c.Stdin
	cmd.Stdout = c.Stdout
	tb := &tailBuffer{max: 8192}
	cmd.Stderr = tb
	err := cmd.Run()
	if err == nil {
		return 0, tb.String(), nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), tb.String(), nil
	}
	if ctx.Err() != nil {
		return -1, tb.String(), ctx.Err()
	}
	return -1, tb.String(), fmt.Errorf("não foi possível executar %s: %w", c.Args[0], err)
}

// DockerRunner executa dentro de um contêiner (docker exec pela Engine API).
type DockerRunner struct {
	C         *dockerapi.Client
	Container string
}

func (d DockerRunner) Where() string { return "contêiner " + d.Container }

func (d DockerRunner) Run(ctx context.Context, c Cmd) (int, string, error) {
	out := c.Stdout
	if out == nil {
		out = io.Discard
	}
	tb := &tailBuffer{max: 8192}
	code, err := d.C.Exec(ctx, d.Container, dockerapi.ExecOptions{Cmd: c.Args, Env: c.Env, Stdin: c.Stdin, Stdout: out, Stderr: tb})
	return code, tb.String(), err
}

// capture roda o comando e devolve o stdout (limitado) — para listagens e consultas curtas.
func capture(ctx context.Context, r Runner, c Cmd) (string, int, string, error) {
	var b bytes.Buffer
	c.Stdout = &limited{w: &b, max: 4 << 20}
	code, se, err := r.Run(ctx, c)
	return strings.TrimSpace(b.String()), code, se, err
}

type limited struct {
	w   *bytes.Buffer
	max int
}

func (l *limited) Write(p []byte) (int, error) {
	if l.w.Len()+len(p) > l.max {
		return len(p), nil // descarta o excesso
	}
	return l.w.Write(p)
}
