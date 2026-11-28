-- Coruja Backup · migração 014: usuário que precisa trocar a senha antes de usar o sistema (o admin/admin da primeira subida).
ALTER TABLE users ADD COLUMN must_change_password TINYINT(1) NOT NULL DEFAULT 0;
