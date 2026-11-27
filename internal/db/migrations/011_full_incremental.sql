-- Coruja Backup · migração 011: backups full + incrementais.
-- backup_mode = full (cada execução é completa) ou incremental (full no dia full_weekday, 0 = domingo; incrementais nos outros).
ALTER TABLE jobs
  ADD COLUMN backup_mode VARCHAR(12) NOT NULL DEFAULT 'full',
  ADD COLUMN full_weekday TINYINT NOT NULL DEFAULT 0;
-- tipo de cada execução: full ou incremental
ALTER TABLE runs ADD COLUMN backup_kind VARCHAR(12) NOT NULL DEFAULT 'full';
