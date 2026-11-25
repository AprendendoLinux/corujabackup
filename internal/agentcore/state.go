package agentcore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/AprendendoLinux/corujabackup/internal/pb"
)

// State guarda no disco do agente: os jobs recebidos (para agendar mesmo com o Server fora do ar), o último horário
// disparado de cada job e a fila de resultados ainda não confirmados pelo Server.
type State struct {
	dir string
	mu  sync.Mutex
}

func NewState(dir string) (*State, error) {
	s := &State{dir: dir}
	if err := os.MkdirAll(filepath.Join(dir, "spool"), 0o700); err != nil {
		return nil, err
	}
	return s, nil
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *State) SaveJobs(jobs []*pb.JobSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := protojson.Marshal(&pb.SyncJobs{Jobs: jobs})
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, "jobs.json"), b)
}

func (s *State) LoadJobs() []*pb.JobSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(s.dir, "jobs.json"))
	if err != nil {
		return nil
	}
	var sj pb.SyncJobs
	if protojson.Unmarshal(b, &sj) != nil {
		return nil
	}
	return sj.Jobs
}

func (s *State) SaveLastFire(m map[int64]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(m)
	return writeAtomic(filepath.Join(s.dir, "lastfire.json"), b)
}

func (s *State) LoadLastFire() map[int64]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := map[int64]int64{}
	if b, err := os.ReadFile(filepath.Join(s.dir, "lastfire.json")); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// ---- fila local de resultados -----------------------------------------------------------------------------------

func (s *State) spoolPath(uid string) string { return filepath.Join(s.dir, "spool", uid+".json") }

func (s *State) SpoolAdd(r *pb.RunReport) error {
	b, err := protojson.Marshal(r)
	if err != nil {
		return err
	}
	return writeAtomic(s.spoolPath(r.RunUid), b)
}

func (s *State) SpoolRemove(uid string) { _ = os.Remove(s.spoolPath(uid)) }

// SpoolList devolve os relatórios pendentes, do mais antigo para o mais novo.
func (s *State) SpoolList() []*pb.RunReport {
	ents, err := os.ReadDir(filepath.Join(s.dir, "spool"))
	if err != nil {
		return nil
	}
	var out []*pb.RunReport
	for _, e := range ents {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, "spool", e.Name()))
		if err != nil {
			continue
		}
		var r pb.RunReport
		if protojson.Unmarshal(b, &r) == nil && r.RunUid != "" {
			out = append(out, &r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FinishedMs < out[j].FinishedMs })
	return out
}
