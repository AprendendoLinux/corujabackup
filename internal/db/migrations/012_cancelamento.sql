-- Coruja Backup · migração 012: pedido de cancelamento guardado na execução.
-- Enquanto o agente interrompe o backup, a execução aparece como "Cancelando" para todos, em qualquer tela.
ALTER TABLE runs ADD COLUMN cancel_requested_at DATETIME(3) NULL;
