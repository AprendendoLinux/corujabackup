-- Coruja Backup · migração 013: formato do backup de arquivos.
-- format = tar (um .tar.gz por execução, como sempre) ou dir (uma pasta com os arquivos abertos; só fora da nuvem).
ALTER TABLE jobs ADD COLUMN format VARCHAR(8) NOT NULL DEFAULT 'tar';
