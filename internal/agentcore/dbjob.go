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
	"github.com/AprendendoLinux/corujabackup/internal/mikrotik"
	"github.com/AprendendoLinux/corujabackup/internal/netdest"
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
	return runDatabase(ctx, name, spec, subdir, dk, backupDir, loc, now, h, nil)
}

// runDatabase: com out (modo direto), o dump é feito numa pasta temporária do agente e o .tar.gz vai direto ao storage.
func runDatabase(ctx context.Context, name string, spec *pb.DbSpec, subdir string, dk *dockerapi.Client, backupDir string, loc *time.Location, now time.Time, h Hooks, out archiveOut) (Result, error) {
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
	dumped, err := dbdump.Run(ctx, cfg, runner, dk, work, dh)
	if err != nil {
		return res, err
	}
	res.Warnings += dumped.Warnings

	switch cfg.RestoreTest {
	case dbdump.RestoreFull:
		h.log("info", "teste de restauração real: restaurando num servidor descartável")
		if err := dbdump.RestoreTest(ctx, dk, cfg, dumped, dh); err != nil {
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

	var srcBytes int64
	for _, f := range dumped.Files {
		srcBytes += f.Bytes
	}
	packed, err := packWork(ctx, work, dest, Slug(name), loc, now, h, out, int64(len(dumped.Files)), srcBytes, "bancos/arquivos")
	packed.Warnings += res.Warnings // avisos do dump e do teste de restauração
	return packed, err
}

// packWork empacota os arquivos de uma pasta de trabalho (sem subpastas) num .tar.gz, confere e o grava na pasta de
// destino do agente ou, com out (modo direto), no storage. Usado pelos jobs de banco e de MikroTik.
func packWork(ctx context.Context, work, dest, prefix string, loc *time.Location, now time.Time, h Hooks, out archiveOut, files, srcBytes int64, what string) (Result, error) {
	var res Result
	local := out == nil
	if local {
		out = &localOut{dest: dest}
	}
	arcName, w, err := out.begin(prefix, loc, now, false)
	if err != nil {
		return res, err
	}
	sum, size, entries, err := packFlat(ctx, work, prefix, w)
	if err != nil {
		out.abort()
		return res, err
	}
	res.OutputName, res.Prefix = arcName, prefix
	res.BytesOutput, res.SHA256, res.Files, res.BytesSource = size, sum, files, srcBytes
	h.log("info", "arquivo gerado: %s (%s, %d %s)", arcName, HumanBytes(size), files, what)
	if !local {
		h.log("info", "conferindo o arquivo no storage")
	}
	if err := out.commit(ctx, sum, entries); err != nil {
		return res, err
	}
	if d, ok := out.(*directOut); ok {
		res.Stored = &d.result
		if d.result.VerifiedBy == "sha256" {
			h.log("info", "conferido no storage: SHA-256 relido e %d itens legíveis", entries)
		} else {
			h.log("info", "conferido no storage pelo tamanho (conferência rápida)")
		}
		return res, nil
	}
	res.OutputPath, res.DestDir = filepath.Join(dest, arcName), dest
	h.log("info", "verificação ok: SHA-256 conferido e %d itens legíveis", entries)
	return res, nil
}

// packFlat empacota os arquivos de dir (sem subpastas) em w como tar.gz, dentro da pasta raiz root/.
func packFlat(ctx context.Context, dir, root string, w io.Writer) (sum string, size int64, entries int64, err error) {
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
	h := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(w, h)}
	gz := gzip.NewWriter(cw)
	tw := tar.NewWriter(gz)
	fail := func(e error) (string, int64, int64, error) { tw.Close(); gz.Close(); return "", 0, 0, e }
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
	case "fs_list":
		var req BrowseRequest
		if err := json.Unmarshal(m.Payload, &req); err != nil {
			return nil, fmt.Errorf("pedido inválido: %w", err)
		}
		if req.Area == BrowseDiskArea {
			root, err := c.writeRoot()
			if err != nil {
				return nil, err
			}
			r, err := BrowseDisk(root, req.Path, c.cfg.BackupRoots)
			if err != nil {
				return nil, err
			}
			return json.Marshal(r)
		}
		var base string
		switch req.Area {
		case BrowseHost:
			base = c.cfg.HostRoot
			if base == "" {
				base = "/"
			}
		case BrowseBackups:
			base = c.cfg.BackupDir
		default:
			return nil, errors.New("área de navegação inválida")
		}
		r, err := Browse(base, req, c.cfg.AllowedPaths)
		if err != nil {
			return nil, err
		}
		return json.Marshal(r)
	case "dest_folders":
		var req struct {
			Config netdest.Config `json:"config"`
			Path   string         `json:"path"`
		}
		if err := json.Unmarshal(m.Payload, &req); err != nil {
			return nil, fmt.Errorf("pedido inválido: %w", err)
		}
		tctx, cancel := context.WithTimeout(ctx, 50*time.Second)
		defer cancel()
		list, err := netdest.Folders(tctx, req.Config, c.destEnv(), req.Path)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"folders": list})
	case "dest_test":
		var cfg netdest.Config
		if err := json.Unmarshal(m.Payload, &cfg); err != nil {
			return nil, fmt.Errorf("pedido inválido: %w", err)
		}
		tctx, cancel := context.WithTimeout(ctx, 75*time.Second)
		defer cancel()
		r, err := netdest.Test(tctx, cfg, c.destEnv())
		if err != nil {
			// a impressão digital vista ajuda a interface a explicar o problema
			return nil, err
		}
		return json.Marshal(r)
	case "mikrotik_test":
		var cfg mikrotik.Config
		if err := json.Unmarshal(m.Payload, &cfg); err != nil {
			return nil, fmt.Errorf("pedido inválido: %w", err)
		}
		tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		info, err := mikrotik.Test(tctx, cfg)
		if err != nil {
			return nil, err
		}
		return json.Marshal(info)
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
