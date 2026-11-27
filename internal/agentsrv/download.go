package agentsrv

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/AprendendoLinux/corujabackup/internal/auth"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

// pendingDownload é um download pedido a um agente, esperando o fluxo dele.
type pendingDownload struct {
	agentID int64
	name    chan string // nome do arquivo (no primeiro pedaço) ou "" com erro em errc
	errc    chan error
	pw      *io.PipeWriter
}

type downloads struct {
	mu sync.Mutex
	m  map[string]*pendingDownload
}

var dls = &downloads{m: map[string]*pendingDownload{}}

// FetchDownload pede ao agente o conteúdo de act (preenche DownloadId) e devolve o fluxo para repassar ao navegador e o
// nome do arquivo. Espera o primeiro pedaço até wait: falhas antes disso (backup sem o item, storage fora) voltam
// como erro, para a interface mostrar a mensagem.
func (s *Server) FetchDownload(ctx context.Context, agentID int64, act *pb.AgentAction, wait time.Duration) (io.ReadCloser, string, error) {
	id := auth.NewUUID()
	act.Kind, act.DownloadId = "download", id
	pr, pw := io.Pipe()
	p := &pendingDownload{agentID: agentID, name: make(chan string, 1), errc: make(chan error, 1), pw: pw}
	dls.mu.Lock()
	dls.m[id] = p
	dls.mu.Unlock()
	drop := func() {
		dls.mu.Lock()
		delete(dls.m, id)
		dls.mu.Unlock()
	}
	if err := s.Action(agentID, act); err != nil {
		drop()
		return nil, "", err
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case name := <-p.name:
		return &closeReader{PipeReader: pr, done: drop}, name, nil
	case err := <-p.errc:
		drop()
		return nil, "", err
	case <-t.C:
		drop()
		pr.CloseWithError(errors.New("tempo esgotado"))
		return nil, "", errors.New("o agente demorou demais para começar o download")
	case <-ctx.Done():
		drop()
		pr.CloseWithError(ctx.Err())
		return nil, "", ctx.Err()
	}
}

type closeReader struct {
	*io.PipeReader
	done func()
}

func (c *closeReader) Close() error {
	c.done()
	return c.PipeReader.CloseWithError(errors.New("download interrompido pelo navegador"))
}

// Download (gRPC) recebe o fluxo do agente e o entrega ao pedido correspondente.
func (s *Server) Download(stream grpc.ClientStreamingServer[pb.DownloadChunk, pb.DownloadDone]) error {
	a, err := s.authenticate(stream.Context())
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	dls.mu.Lock()
	p := dls.m[first.Id]
	dls.mu.Unlock()
	if p == nil || p.agentID != a.ID {
		return status.Error(codes.NotFound, "download desconhecido")
	}
	if first.Error != "" {
		p.errc <- errors.New(first.Error)
		return stream.SendAndClose(&pb.DownloadDone{})
	}
	p.name <- first.FileName
	if _, err := p.pw.Write(first.Data); err != nil {
		return status.Error(codes.Canceled, "o navegador desistiu do download")
	}
	for {
		m, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			p.pw.Close()
			return stream.SendAndClose(&pb.DownloadDone{})
		}
		if err != nil {
			p.pw.CloseWithError(errors.New("o agente interrompeu o download"))
			return err
		}
		if m.Error != "" {
			p.pw.CloseWithError(errors.New(m.Error))
			return stream.SendAndClose(&pb.DownloadDone{})
		}
		if _, err := p.pw.Write(m.Data); err != nil { // bloqueia enquanto o navegador não lê: o HTTP/2 segura o agente
			return status.Error(codes.Canceled, "o navegador desistiu do download")
		}
	}
}
