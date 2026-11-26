package cloudmgr

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/AprendendoLinux/corujabackup/internal/netdest"
)

// Destinos de rede (SFTP, FTP, SMB, pasta local) são guardados junto das contas de nuvem, com a configuração cifrada, mas
// quem os opera é o agente: o Server não os alcança necessariamente e não fala esses protocolos.

// ErrAgentDestination: a operação pedida só pode ser feita pelo agente.
var ErrAgentDestination = errors.New("este destino é operado pelo agente (não há acesso a partir do Server)")

// CreateDestination guarda um destino já testado por um agente.
func (m *Manager) CreateDestination(ctx context.Context, name string, cfg netdest.Config) (int64, error) {
	if err := cfg.Normalize(); err != nil {
		return 0, err
	}
	if cfg.Type == netdest.TypeSFTP && cfg.HostKey == "" {
		return 0, errors.New("teste o destino antes de salvar: a impressão digital do servidor SFTP precisa ser confirmada")
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return 0, err
	}
	sealed, err := m.Box.Seal(string(b))
	if err != nil {
		return 0, err
	}
	id, err := m.Store.CreateCloud(ctx, name, cfg.Type, sealed, "")
	if err != nil {
		return 0, err
	}
	return id, m.Store.SetCloudTokens(ctx, id, "", cfg.Label())
}

// DestConfig devolve a configuração de um destino de rede; (nil, nil) se a conta for uma nuvem.
func (m *Manager) DestConfig(ctx context.Context, id int64) (*netdest.Config, error) {
	a, err := m.Store.GetCloud(ctx, id)
	if err != nil {
		return nil, err
	}
	if !netdest.Is(a.Type) {
		return nil, nil
	}
	plain, err := m.Box.Open(a.Config)
	if err != nil {
		return nil, err
	}
	var c netdest.Config
	if err := json.Unmarshal([]byte(plain), &c); err != nil {
		return nil, err
	}
	return &c, nil
}
