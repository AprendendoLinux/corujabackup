-- Coruja Backup · migração 005: jobs de banco de dados (configuração cifrada, com credenciais)
ALTER TABLE jobs ADD COLUMN db_config MEDIUMTEXT NULL;
