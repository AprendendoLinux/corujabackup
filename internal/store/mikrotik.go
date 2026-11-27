package store

import (
	"encoding/json"
	"errors"

	"github.com/AprendendoLinux/corujabackup/internal/mikrotik"
	"github.com/AprendendoLinux/corujabackup/internal/secrets"
)

// Num job de MikroTik, a configuração do roteador (com a senha de acesso e a do .backup) fica cifrada na mesma coluna
// usada pela configuração dos jobs de banco (db_config): cada job tem um tipo só.

// OpenMikrotik decifra a configuração do roteador (nil se o job não tiver).
func (j *Job) OpenMikrotik(box *secrets.Box) (*mikrotik.Config, error) {
	if j.Kind != "mikrotik" || j.DBSealed == "" {
		return nil, nil
	}
	if box == nil {
		return nil, errors.New("chave de criptografia indisponível")
	}
	plain, err := box.Open(j.DBSealed)
	if err != nil {
		return nil, err
	}
	var c mikrotik.Config
	if err := json.Unmarshal([]byte(plain), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// SealMikrotik cifra a configuração do roteador para guardar no job.
func SealMikrotik(box *secrets.Box, c mikrotik.Config) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return box.Seal(string(b))
}
