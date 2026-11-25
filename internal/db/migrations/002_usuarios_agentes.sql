-- Coruja Backup · migração 002: usuários, sessões, matrícula/credenciais de agentes e auditoria
CREATE TABLE users (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  username VARCHAR(64) NOT NULL,
  display_name VARCHAR(100) NOT NULL DEFAULT '',
  password_hash VARCHAR(255) NOT NULL,
  role ENUM('admin','operator','viewer') NOT NULL DEFAULT 'viewer',
  totp_secret VARCHAR(64) NULL,
  totp_enabled TINYINT(1) NOT NULL DEFAULT 0,
  totp_last_counter BIGINT NOT NULL DEFAULT 0,
  recovery_codes TEXT NULL,
  disabled TINYINT(1) NOT NULL DEFAULT 0,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_login_at TIMESTAMP NULL,
  UNIQUE KEY uq_users_username (username)
) ENGINE=InnoDB;

CREATE TABLE sessions (
  id CHAR(64) NOT NULL PRIMARY KEY,
  user_id BIGINT NOT NULL,
  pending_2fa TINYINT(1) NOT NULL DEFAULT 0,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP NOT NULL,
  last_seen_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  ip VARCHAR(45) NOT NULL DEFAULT '',
  user_agent VARCHAR(255) NOT NULL DEFAULT '',
  KEY ix_sessions_user (user_id),
  KEY ix_sessions_expires (expires_at),
  CONSTRAINT fk_sessions_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE agents (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  hostname VARCHAR(255) NOT NULL DEFAULT '',
  os VARCHAR(40) NOT NULL DEFAULT '',
  arch VARCHAR(20) NOT NULL DEFAULT '',
  agent_version VARCHAR(40) NOT NULL DEFAULT '',
  docker_available TINYINT(1) NOT NULL DEFAULT 0,
  ip VARCHAR(45) NOT NULL DEFAULT '',
  credential_hash CHAR(64) NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen_at TIMESTAMP NULL,
  revoked_at TIMESTAMP NULL,
  UNIQUE KEY uq_agents_name (name)
) ENGINE=InnoDB;

CREATE TABLE enrollment_tokens (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  token_hash CHAR(64) NOT NULL,
  name VARCHAR(100) NOT NULL,
  created_by BIGINT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP NOT NULL,
  used_at TIMESTAMP NULL,
  agent_id BIGINT NULL,
  UNIQUE KEY uq_enroll_hash (token_hash),
  CONSTRAINT fk_enroll_agent FOREIGN KEY (agent_id) REFERENCES agents (id) ON DELETE SET NULL
) ENGINE=InnoDB;

CREATE TABLE audit_log (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  user_id BIGINT NULL,
  username VARCHAR(64) NOT NULL DEFAULT '',
  action VARCHAR(60) NOT NULL,
  detail VARCHAR(500) NOT NULL DEFAULT '',
  ip VARCHAR(45) NOT NULL DEFAULT '',
  KEY ix_audit_at (at)
) ENGINE=InnoDB;
