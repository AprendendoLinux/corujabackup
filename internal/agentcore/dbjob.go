package agentcore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AprendendoLinux/corujabackup/internal/dbdump"
	"github.com/AprendendoLinux/corujabackup/internal/dockerapi"
	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

func randName() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// DBConfigFrom converte a especificação recebida do Server na configuração do pacote dbdump.
func DBConfigFrom(d *pb.DbSpec) dbdump.Config {
	if d == nil {
		return dbdump.Config{}
	}
	return dbdump.Config{Engine: d.Engine, Mode: d.Mode, Container: d.Container, Host: d.Host, Port: int(d.Port), User: d.User, Password: d.Password,
		AuthDB: d.AuthDb, Databases: d.Databases, Exclude: d.ExcludeDatabases, SSLSkipVerify: d.SslSkipVerify, RestoreTest: d.RestoreTest, RestoreImage: d.RestoreImage}
}

// RunDatabase faz o dump dos bancos, confere (estrutura e, se configurado, restauração real num servidor descartável),
// empacota tudo em <slug>-AAAAMMDD-HHMMSS.tar.gz e verifica o arquivo. Segue o mesmo contrato de RunFiles.
func RunDatabase(ctx context.Context, name string, spec *pb.DbSpec, subdir string, dk *dockerapi.Client, backupDir string, loc *time.Location, now time.Time, h Hooks) (Result, error) {
	var res Result
	if spec == nil {
		return res, errors.New("job de banco sem configuração")
	}
	dest, err := SafeSubdir(backupDir, subdir)
	if err != nil {
		return res, err
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return res, fmt.Errorf("não foi possível criar a pasta de destino: %w", err)
	}
	work := filepath.Join(dest, ".tmp-"+randName())
	defer os.RemoveAll(work)

	cfg := DBConfigFrom(spec)
	runner, err := dbdump.ResolveRunner(ctx, &cfg, dk)
	if err != nil {
		return res, err
	}
	dh := dbdump.Hooks{Logf: h.Logf}
	out, err := dbdump.Run(ctx, cfg, runner, dk, work, dh)
	if err != nil {
		return res, err
	}
	res.Warnings += out.Warnings

	switch cfg.RestoreTest {
	case dbdump.RestoreFull:
		h.log("info", "teste de restauração real: restaurando num servidor descartável")
		if err := dbdump.RestoreTest(ctx, dk, cfg, out, dh); err != nil {
			if ctx.Err() != nil {
				return res, ErrCanceled
			}
			res.Warnings++
			h.log("error", "TESTE DE RESTAURAÇÃO FALHOU: %v", err)
		} else {
			h.log("info", "teste de restauração real concluído: o backup se restaura e confere com a origem")
		}
	default:
		h.log("info", "conferência estrutural concluída (para provar a restauração, ative o teste de restauração real no job)")
	}

	prefix := Slug(name)
	arcName, final, tmp := nextArchiveName(dest, prefix, loc, now)
	defer os.Remove(tmp)
	sum, size, entries, err := packFlat(ctx, work, prefix, tmp)
	if err != nil {
		return res, err
	}
	if err := os.Rename(tmp, final); err != nil {
		return res, err
	}
	res.OutputName, res.OutputPath, res.DestDir, res.Prefix = arcName, final, dest, prefix
	res.BytesOutput, res.SHA256, res.Files = size, sum, int64(len(out.Files))
	for _, f := range out.Files {
		res.BytesSource += f.Bytes
	}
	h.log("info", "arquivo gerado: %s (%s, %d bancos/arquivos)", arcName, HumanBytes(size), len(out.Files))
	if err := VerifyArchive(ctx, final, sum, entries); err != nil {
		os.Remove(final)
		return res, fmt.Errorf("verificação falhou (arquivo removido): %w", err)
	}
	h.log("info", "verificação ok: SHA-256 conferido e %d itens legíveis", entries)
	return res, nil
}

// nextArchiveName gera o nome <prefixo>-AAAAMMDD-HHMMSS.tar.gz sem colidir com um arquivo existente.
func nextArchiveName(dest, prefix string, loc *time.Location, now time.Time) (name, final, tmp string) {
	for i := 0; i < 120; i++ {
		name = fmt.Sprintf("%s-%s.tar.gz", prefix, now.In(loc).Format("20060102-150405"))
		if _, err := os.Lstat(filepath.Join(dest, name)); err != nil {
			break
		}
		now = now.Add(time.Second)
	}
	return name, filepath.Join(dest, name), filepath.Join(dest, "."+name+".tmp")
}

// packFlat empacota os arquivos de dir (sem subpastas) em tmp como tar.gz, dentro da pasta raiz root/.
func packFlat(ctx context.Context, dir, root, tmp string) (sum string, size int64, entries int64, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, 0, err
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return "", 0, 0, err
	}
	h := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(f, h)}
	gz := gzip.NewWriter(cw)
	tw := tar.NewWriter(gz)
	fail := func(e error) (string, int64, int64, error) { tw.Close(); gz.Close(); f.Close(); return "", 0, 0, e }
	for _, n := range names {
		if ctx.Err() != nil {
			return fail(ErrCanceled)
		}
		src, err := os.Open(filepath.Join(dir, n))
		if err != nil {
			return fail(err)
		}
		st, err := src.Stat()
		if err != nil {
			src.Close()
			return fail(err)
		}
		hdr := &tar.Header{Name: root + "/" + n, Mode: 0o640, Size: st.Size(), ModTime: st.ModTime(), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			src.Close()
			return fail(err)
		}
		_, err = io.Copy(tw, src)
		src.Close()
		if err != nil {
			return fail(err)
		}
		entries++
	}
	if entries == 0 {
		return fail(errors.New("nenhum arquivo para empacotar"))
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return "", 0, 0, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", 0, 0, err
	}
	if err := f.Close(); err != nil {
		return "", 0, 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), cw.n, entries, nil
}

// ---------------------------------------------------------------- chamadas do Server (RPC)

type ContainerInfo struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	Status string `json:"status"`
	Engine string `json:"engine"` // palpite pelo nome da imagem ("" se não parecer um banco)
}

func guessEngine(image string) string {
	i := strings.ToLower(image)
	if k := strings.LastIndex(i, "/"); k >= 0 {
		i = i[k+1:]
	}
	switch {
	case strings.HasPrefix(i, "mariadb"):
		return dbdump.EngineMariaDB
	case strings.HasPrefix(i, "mysql") || strings.HasPrefix(i, "percona"):
		return dbdump.EngineMySQL
	case strings.HasPrefix(i, "postgres") || strings.HasPrefix(i, "timescaledb") || strings.HasPrefix(i, "postgis"):
		return dbdump.EnginePostgres
	case strings.HasPrefix(i, "mongo"):
		return dbdump.EngineMongo
	}
	return ""
}

// HandleRPC atende as chamadas do Server que precisam de resposta.
func (c *Core) HandleRPC(ctx context.Context, m *pb.Rpc) *pb.AgentMessage {
	res := &pb.RpcResult{Id: m.Id}
	payload, err := c.rpc(ctx, m)
	if err != nil {
		res.Error = err.Error()
	} else {
		res.Ok, res.Payload = true, payload
	}
	return &pb.AgentMessage{Body: &pb.AgentMessage_RpcResult{RpcResult: res}}
}

func (c *Core) rpc(ctx context.Context, m *pb.Rpc) ([]byte, error) {
	switch m.Kind {
	case "containers":
		if !c.docker.Available(ctx) {
			return nil, errors.New("o Docker não está acessível no agente (monte /var/run/docker.sock no contêiner do agente)")
		}
		list, err := c.docker.List(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]ContainerInfo, 0, len(list))
		for _, ct := range list {
			if !ContainerAllowed(c.cfg.AllowedContainers, ct.Name()) {
				continue
			}
			out = append(out, ContainerInfo{Name: ct.Name(), Image: ct.Image, Status: ct.Status, Engine: guessEngine(ct.Image)})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return json.Marshal(map[string]any{"containers": out})
	case "db_test":
		var d pb.DbSpec
		if err := json.Unmarshal(m.Payload, &d); err != nil {
			return nil, fmt.Errorf("pedido inválido: %w", err)
		}
		cfg := DBConfigFrom(&d)
		if cfg.Mode == "docker" && !ContainerAllowed(c.cfg.AllowedContainers, cfg.Container) {
			return nil, fmt.Errorf("este agente não tem permissão para acessar o contêiner %q (CORUJA_ALLOWED_CONTAINERS)", cfg.Container)
		}
		tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		r, err := dbdump.ResolveRunner(tctx, &cfg, c.docker)
		if err != nil {
			return nil, err
		}
		dbs, ver, err := dbdump.ListDatabases(tctx, cfg, r)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"databases": dbs, "version": ver, "user": cfg.User, "where": r.Where()})
	}
	return nil, fmt.Errorf("chamada desconhecida: %s", m.Kind)
}
