package store

import (
	"encoding/json"
	"errors"

	"github.com/AprendendoLinux/corujabackup/internal/secrets"
)

// DBConfig é a configuração de um job de banco de dados. Fica cifrada no banco (contém a senha).
type DBConfig struct {
	Engine           string   `json:"engine"`
	Mode             string   `json:"mode"`
	Container        string   `json:"container"`
	Host             string   `json:"host"`
	Port             int      `json:"port"`
	User             string   `json:"user"`
	Password         string   `json:"password,omitempty"`
	AuthDB           string   `json:"auth_db"`
	Databases        []string `json:"databases"`
	ExcludeDatabases []string `json:"exclude_databases"`
	SSLSkipVerify    bool     `json:"ssl_skip_verify"`
	RestoreTest      string   `json:"restore_test"`
	RestoreImage     string   `json:"restore_image"`
}

// OpenDB decifra a configuração do job (nil se o job não for de banco).
func (j *Job) OpenDB(box *secrets.Box) (*DBConfig, error) {
	if j.DBSealed == "" || j.Kind == "mikrotik" { // num job de MikroTik a coluna guarda o roteador
		return nil, nil
	}
	if box == nil {
		return nil, errors.New("chave de criptografia indisponível")
	}
	plain, err := box.Open(j.DBSealed)
	if err != nil {
		return nil, err
	}
	var c DBConfig
	if err := json.Unmarshal([]byte(plain), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *DBConfig) Seal(box *secrets.Box) (string, error) {
	b, _ := json.Marshal(c)
	return box.Seal(string(b))
}
