-- Coruja Backup · migração 008: o Server conecta nos agentes (endereço host:porta de cada agente e de cada token pendente)
ALTER TABLE agents ADD COLUMN address VARCHAR(300) NOT NULL DEFAULT '';
ALTER TABLE enrollment_tokens ADD COLUMN address VARCHAR(300) NOT NULL DEFAULT '';
