-- Coruja Backup · migração 007: foto (avatar) de cada usuário
ALTER TABLE users
  ADD COLUMN avatar MEDIUMBLOB NULL,
  ADD COLUMN avatar_type VARCHAR(40) NOT NULL DEFAULT '',
  ADD COLUMN avatar_version VARCHAR(16) NOT NULL DEFAULT '';
