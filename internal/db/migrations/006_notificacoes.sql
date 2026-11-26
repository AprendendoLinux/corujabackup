-- Coruja Backup · migração 006: canais (e-mail, Telegram), regras de notificação e fila/histórico de envios
CREATE TABLE notification_channels (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  type VARCHAR(12) NOT NULL,
  config MEDIUMTEXT NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_channel_name (name)
) ENGINE=InnoDB;

CREATE TABLE notification_rules (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  event VARCHAR(20) NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  job_ids TEXT NOT NULL,
  channel_ids TEXT NOT NULL,
  params TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_rule_name (name)
) ENGINE=InnoDB;

CREATE TABLE notifications (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  rule_id BIGINT NULL,
  channel_id BIGINT NULL,
  kind VARCHAR(12) NOT NULL,
  ref VARCHAR(100) NOT NULL,
  subject VARCHAR(255) NOT NULL DEFAULT '',
  status VARCHAR(10) NOT NULL DEFAULT 'pending',
  attempts INT NOT NULL DEFAULT 0,
  error VARCHAR(500) NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  next_attempt_at DATETIME NOT NULL,
  sent_at DATETIME NULL,
  UNIQUE KEY uq_notif (rule_id, channel_id, kind, ref),
  KEY ix_notif_pending (status, next_attempt_at),
  KEY ix_notif_created (created_at),
  CONSTRAINT fk_notif_rule FOREIGN KEY (rule_id) REFERENCES notification_rules (id) ON DELETE CASCADE,
  CONSTRAINT fk_notif_channel FOREIGN KEY (channel_id) REFERENCES notification_channels (id) ON DELETE CASCADE
) ENGINE=InnoDB;
