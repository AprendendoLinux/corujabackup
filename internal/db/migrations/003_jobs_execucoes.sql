-- Coruja Backup · migração 003: jobs de backup (arquivos), execuções e logs
CREATE TABLE jobs (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  agent_id BIGINT NOT NULL,
  kind VARCHAR(20) NOT NULL DEFAULT 'files',
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  cron VARCHAR(100) NOT NULL,
  timezone VARCHAR(64) NOT NULL DEFAULT 'America/Sao_Paulo',
  paths TEXT NOT NULL,
  excludes TEXT NOT NULL,
  dest_subdir VARCHAR(255) NOT NULL DEFAULT '',
  keep_copies INT NOT NULL DEFAULT 7,
  version INT NOT NULL DEFAULT 1,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_jobs_agent_name (agent_id, name),
  CONSTRAINT fk_jobs_agent FOREIGN KEY (agent_id) REFERENCES agents (id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE runs (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  uid CHAR(36) NOT NULL,
  job_id BIGINT NOT NULL,
  agent_id BIGINT NOT NULL,
  trigger_kind VARCHAR(12) NOT NULL DEFAULT 'schedule',
  scheduled_for DATETIME NULL,
  status ENUM('queued','running','ok','warning','failed','canceled','missed') NOT NULL DEFAULT 'queued',
  started_at DATETIME(3) NULL,
  finished_at DATETIME(3) NULL,
  bytes_source BIGINT NOT NULL DEFAULT 0,
  bytes_output BIGINT NOT NULL DEFAULT 0,
  files BIGINT NOT NULL DEFAULT 0,
  sha256 CHAR(64) NOT NULL DEFAULT '',
  output_name VARCHAR(255) NOT NULL DEFAULT '',
  error TEXT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uq_runs_uid (uid),
  UNIQUE KEY uq_runs_job_sched (job_id, scheduled_for),
  KEY ix_runs_created (created_at),
  KEY ix_runs_job (job_id, id),
  CONSTRAINT fk_runs_job FOREIGN KEY (job_id) REFERENCES jobs (id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE run_logs (
  run_id BIGINT NOT NULL,
  seq INT NOT NULL,
  at DATETIME(3) NOT NULL,
  level VARCHAR(8) NOT NULL DEFAULT 'info',
  text VARCHAR(1000) NOT NULL,
  PRIMARY KEY (run_id, seq),
  CONSTRAINT fk_logs_run FOREIGN KEY (run_id) REFERENCES runs (id) ON DELETE CASCADE
) ENGINE=InnoDB;
