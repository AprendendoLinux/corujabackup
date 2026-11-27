-- Coruja Backup · migração 010: gravar o backup direto no storage (sem arquivo local no agente).
-- Só vale para storages operados pelo agente (SFTP, FTP, SMB, NFS e pasta local).
ALTER TABLE jobs ADD COLUMN cloud_direct TINYINT(1) NOT NULL DEFAULT 0;
