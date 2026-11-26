package agentsrv

// Conexão invertida: é o Server quem abre a conexão TCP com cada agente (no host:porta cadastrado na interface). Sobre
// ela, os papéis continuam os mesmos de antes: o Server é o lado servidor do TLS e do gRPC (o agente confere a impressão
// digital do certificado do Server) e o agente se autentica com o token de matrícula ou a credencial dele. Assim o
// protocolo, a autenticação e a fila de resultados não mudam; só muda quem disca.

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/store"
)

// LinkState é a situação da conexão com um endereço.
type LinkState struct {
	Address string    `json:"address"`
	State   string    `json:"state"` // connecting | connected | error
	Error   string    `json:"error,omitempty"`
	Since   time.Time `json:"since"`
	NextTry time.Time `json:"next_try,omitempty"`
}

// Dialer mantém uma conexão aberta com cada endereço cadastrado e entrega as conexões ao servidor gRPC como se fossem
// conexões recebidas (implementa net.Listener).
type Dialer struct {
	Targets func(ctx context.Context) ([]store.DialTarget, error)
	Dial    func(ctx context.Context, address string) (net.Conn, error)
	Every   time.Duration // intervalo entre as varreduras (padrão 3 s)

	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once

	mu     sync.Mutex
	links  map[string]*LinkState
	active map[string]bool
	fails  map[string]int
	wake   chan struct{}
}

func NewDialer(targets func(ctx context.Context) ([]store.DialTarget, error)) *Dialer {
	d := &Dialer{Targets: targets, conns: make(chan net.Conn), closed: make(chan struct{}), links: map[string]*LinkState{},
		active: map[string]bool{}, fails: map[string]int{}, wake: make(chan struct{}, 1)}
	d.Dial = func(ctx context.Context, address string) (net.Conn, error) {
		nd := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		return nd.DialContext(ctx, "tcp", address)
	}
	return d
}

// Accept entrega a próxima conexão aberta pelo Server (para o servidor gRPC).
func (d *Dialer) Accept() (net.Conn, error) {
	select {
	case c := <-d.conns:
		return c, nil
	case <-d.closed:
		return nil, net.ErrClosed
	}
}

func (d *Dialer) Close() error   { d.once.Do(func() { close(d.closed) }); return nil }
func (d *Dialer) Addr() net.Addr { return &net.TCPAddr{} }

// Wake faz uma varredura imediata (ex.: logo depois de cadastrar um agente).
func (d *Dialer) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Links devolve a situação de cada endereço conhecido.
func (d *Dialer) Links() map[string]LinkState {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]LinkState, len(d.links))
	for k, v := range d.links {
		out[k] = *v
	}
	return out
}

func backoff(fails int) time.Duration {
	// curta de propósito: quem acabou de subir o agente não deve esperar; uma tentativa a cada 15 s é custo desprezível
	b := 3 * time.Second << min(fails, 3)
	return min(b, 15*time.Second)
}

// Run varre os endereços e disca os que estão sem conexão, respeitando uma espera crescente após falhas.
func (d *Dialer) Run(ctx context.Context) {
	every := d.Every
	if every <= 0 {
		every = 3 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		d.scan(ctx)
		select {
		case <-ctx.Done():
			_ = d.Close()
			return
		case <-t.C:
		case <-d.wake:
		}
	}
}

func (d *Dialer) scan(ctx context.Context) {
	targets, err := d.Targets(ctx)
	if err != nil {
		return
	}
	want := map[string]bool{}
	now := time.Now()
	d.mu.Lock()
	for _, tg := range targets {
		want[tg.Address] = true
		if d.active[tg.Address] {
			continue
		}
		l := d.links[tg.Address]
		if l != nil && now.Before(l.NextTry) {
			continue
		}
		if l == nil {
			l = &LinkState{Address: tg.Address, Since: now}
			d.links[tg.Address] = l
		}
		if l.State != "error" {
			l.State, l.Since = "connecting", now
		}
		d.active[tg.Address] = true
		go d.dialOne(ctx, tg.Address)
	}
	for a := range d.links { // endereços que saíram do cadastro
		if !want[a] && !d.active[a] {
			delete(d.links, a)
			delete(d.fails, a)
		}
	}
	d.mu.Unlock()
}

func (d *Dialer) dialOne(ctx context.Context, address string) {
	c, err := d.Dial(ctx, address)
	d.mu.Lock()
	l := d.links[address]
	if l == nil {
		l = &LinkState{Address: address}
		d.links[address] = l
	}
	if err != nil {
		d.active[address] = false
		d.fails[address]++
		msg := describeDialErr(err)
		if l.State != "error" || l.Error != msg {
			slog.Warn("não foi possível conectar no agente", "endereço", address, "erro", msg)
			l.Since = time.Now()
		}
		l.State, l.Error, l.NextTry = "error", msg, time.Now().Add(backoff(d.fails[address]))
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()
	tc := &trackedConn{Conn: c, remote: &DialedAddr{Target: address, Addr: c.RemoteAddr()}, done: func(ok bool) { d.released(address, ok) }}
	select {
	case d.conns <- tc:
	case <-d.closed:
		_ = c.Close()
	}
}

// connected é chamado pelo servidor quando a sessão do agente nesta conexão foi autenticada.
func (d *Dialer) connected(address string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if l := d.links[address]; l != nil {
		l.State, l.Error, l.Since = "connected", "", time.Now()
	}
	d.fails[address] = 0
}

func (d *Dialer) released(address string, _ bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.active[address] = false
	if l := d.links[address]; l != nil && l.State == "connected" {
		l.State, l.Since, l.NextTry = "connecting", time.Now(), time.Now().Add(time.Second)
		return
	}
	// a conexão abriu, mas a sessão não se estabeleceu (TLS ou autenticação recusados): espera antes de tentar de novo
	d.fails[address]++
	if l := d.links[address]; l != nil {
		if l.State != "error" {
			l.State, l.Error, l.Since = "error", "o agente aceitou a conexão, mas não concluiu a autenticação (token ou impressão digital do Server não conferem?)", time.Now()
		}
		l.NextTry = time.Now().Add(backoff(d.fails[address]))
	}
}

func describeDialErr(err error) string {
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout():
		return "tempo esgotado: o agente não respondeu (firewall bloqueando a porta, endereço errado ou máquina desligada)"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "conexão recusada: nada escutando nessa porta (o agente está rodando e com a porta publicada?)"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "nome não encontrado: " + dnsErr.Name
	}
	return err.Error()
}

// DialedAddr é o endereço remoto de uma conexão aberta pelo Server: guarda também o endereço cadastrado (que pode ser um
// nome) para a sessão saber de qual cadastro ela veio.
type DialedAddr struct {
	Target string
	net.Addr
}

type trackedConn struct {
	net.Conn
	remote *DialedAddr
	once   sync.Once
	done   func(ok bool)
}

func (c *trackedConn) RemoteAddr() net.Addr { return c.remote }

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.done(true) })
	return err
}

// Links devolve a situação das conexões abertas pelo Server (vazio sem discador).
func (s *Server) Links() map[string]LinkState {
	if s.Dialer == nil {
		return map[string]LinkState{}
	}
	return s.Dialer.Links()
}

// Wake pede uma tentativa de conexão imediata (ex.: logo depois de cadastrar ou mudar o endereço de um agente).
func (s *Server) Wake() {
	if s.Dialer != nil {
		s.Dialer.Wake()
	}
}
