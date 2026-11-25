-- Coruja Backup · migração 001: configurações gerais (marca/tema/logo etc.)
CREATE TABLE settings (
  k VARCHAR(100) NOT NULL PRIMARY KEY,
  v MEDIUMTEXT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB;

INSERT INTO settings (k, v) VALUES ('brand.name', 'Coruja Backup'), ('brand.theme', 'coruja');
