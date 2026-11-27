-- Coruja Backup · migração 009: nome e sobrenome de cada usuário.
-- display_name continua existindo (e-mails, auditoria), sempre igual a "nome sobrenome".
ALTER TABLE users
  ADD COLUMN first_name VARCHAR(60) NOT NULL DEFAULT '',
  ADD COLUMN last_name VARCHAR(60) NOT NULL DEFAULT '';
UPDATE users SET
  first_name = LEFT(SUBSTRING_INDEX(TRIM(display_name), ' ', 1), 60),
  last_name = LEFT(TRIM(SUBSTR(TRIM(display_name), CHAR_LENGTH(SUBSTRING_INDEX(TRIM(display_name), ' ', 1)) + 1)), 60);
