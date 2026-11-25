# Roadmap

- [x] **Fase 0 – Fundação (0.0.1):** repositório, Server/Agente Go, UI React com temas, MySQL + migrações, Docker, CI.
- [x] **Fase 1 – Núcleo do Server (0.0.2):** usuários e perfis (login, 2FA), matrícula de agentes por token, canal gRPC/TLS, API REST.
- [x] **Fase 2 – Agente e arquivos (0.0.3):** cadastro/status de clients, jobs de pastas (tar+gzip), agendamento com fuso, batimento, fila local.
- [x] **Fase 3 – Conectores de nuvem (0.0.4):** interface `Provider`, OneDrive, Google Drive, verificação por hash, retenção segura.
- [ ] **Fase 4 – Bancos de dados:** MySQL/MariaDB, PostgreSQL, MongoDB; teste de restauração automatizado.
- [ ] **Fase 5 – Interface completa:** servidores, jobs, histórico, logs ao vivo, temas, logo personalizada, restauração.
- [ ] **Fase 6 – Notificações e relatórios:** e-mail, Telegram, PDF A4, configurados pela interface.
- [ ] **Fase 7 – Endurecimento e publicação:** TLS, auditoria, limites, documentação, imagens no GitHub Packages.
- [ ] **Fase 8 – Piloto:** instalação em ambiente real, só com autorização e em paralelo ao backup existente.
