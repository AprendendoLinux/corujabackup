package agentsrv

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/AprendendoLinux/corujabackup/internal/pb"
	"github.com/AprendendoLinux/corujabackup/internal/store"
)

const fetchChunk = 1 << 20

// FetchBackup (gRPC) entrega ao agente um arquivo de backup que só está na nuvem (restauração, extração, download), ou
// só um trecho dele (offset/length): com o índice de blocos do catálogo, o agente pede só o que precisa.
// O agente só recebe arquivos dos próprios jobs, e só os que a execução registrou como enviados à nuvem.
func (s *Server) FetchBackup(req *pb.FetchRequest, stream grpc.ServerStreamingServer[pb.FetchChunk]) error {
	ctx := stream.Context()
	a, err := s.authenticate(ctx)
	if err != nil {
		return err
	}
	if s.Cloud == nil {
		return status.Error(codes.Unavailable, "o Server não tem acesso às contas de nuvem")
	}
	job, err := s.Store.GetJob(ctx, req.JobId)
	if err != nil || job.AgentID != a.ID {
		return status.Error(codes.NotFound, "job desconhecido")
	}
	if job.CloudAccountID == nil {
		return status.Error(codes.FailedPrecondition, "este job não envia para a nuvem")
	}
	run, err := s.Store.RunByOutput(ctx, job.ID, req.FileName)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (run.CloudStatus != "ok" || run.CloudPath == "")) {
		return status.Error(codes.NotFound, fmt.Sprintf("%s não consta como enviado à nuvem", req.FileName))
	}
	if err != nil {
		return status.Error(codes.Internal, "falha ao consultar o histórico")
	}
	if req.Offset < 0 || req.Length < 0 {
		return status.Error(codes.InvalidArgument, "trecho inválido")
	}
	folder, name := path.Split(run.CloudPath)
	body, size, err := s.Cloud.OpenRange(ctx, *job.CloudAccountID, strings.TrimSuffix(folder, "/"), name, req.Offset, req.Length)
	if err != nil {
		slog.Warn("não foi possível ler da nuvem para o agente", "agente", a.Name, "arquivo", run.CloudPath, "erro", err.Error())
		return status.Error(codes.Unavailable, "não foi possível ler "+name+" da nuvem: "+err.Error())
	}
	defer body.Close()
	slog.Info("entregando arquivo da nuvem ao agente", "agente", a.Name, "arquivo", run.CloudPath, "início", req.Offset, "bytes", size)
	if err := stream.Send(&pb.FetchChunk{Size: size}); err != nil {
		return err
	}
	buf := make([]byte, fetchChunk)
	for {
		n, rerr := io.ReadFull(body, buf)
		if n > 0 {
			if err := stream.Send(&pb.FetchChunk{Data: buf[:n]}); err != nil { // o HTTP/2 segura a leitura se o agente for lento
				return err
			}
		}
		if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
			return nil
		}
		if rerr != nil {
			return status.Error(codes.Unavailable, "a leitura da nuvem falhou: "+rerr.Error())
		}
	}
}
