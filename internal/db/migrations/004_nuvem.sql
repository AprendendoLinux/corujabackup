-- Coruja Backup · migração 004: contas de nuvem, destino de nuvem nos jobs e dados de envio nas execuções
CREATE TABLE cloud_accounts (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  type VARCHAR(20) NOT NULL,
  status VARCHAR(12) NOT NULL DEFAULT 'pending',
  config MEDIUMTEXT NOT NULL,
  tokens MEDIUMTEXT NULL,
  device MEDIUMTEXT NULL,
  account_label VARCHAR(255) NOT NULL DEFAULT '',
  quota_total BIGINT NOT NULL DEFAULT 0,
  quota_used BIGINT NOT NULL DEFAULT 0,
  quota_checked_at DATETIME NULL,
  last_error VARCHAR(500) NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_cloud_name (name)
) ENGINE=InnoDB;

ALTER TABLE jobs
  ADD COLUMN cloud_account_id BIGINT NULL,
  ADD COLUMN cloud_folder VARCHAR(500) NOT NULL DEFAULT '',
  ADD COLUMN cloud_keep INT NOT NULL DEFAULT 0,
  ADD COLUMN cloud_keep_days INT NOT NULL DEFAULT 0,
  ADD CONSTRAINT fk_jobs_cloud FOREIGN KEY (cloud_account_id) REFERENCES cloud_accounts (id) ON DELETE RESTRICT;

ALTER TABLE runs
  ADD COLUMN phase VARCHAR(20) NOT NULL DEFAULT '',
  ADD COLUMN progress_pct INT NOT NULL DEFAULT 0,
  ADD COLUMN cloud_status VARCHAR(10) NOT NULL DEFAULT 'none',
  ADD COLUMN cloud_path VARCHAR(700) NOT NULL DEFAULT '',
  ADD COLUMN cloud_hash VARCHAR(100) NOT NULL DEFAULT '',
  ADD COLUMN cloud_verified_by VARCHAR(12) NOT NULL DEFAULT '';
