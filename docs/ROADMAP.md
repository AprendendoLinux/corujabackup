# Roadmap

- [x] **Fase 0 – Fundação (0.0.1):** repositório, Server/Agente Go, UI React com temas, MySQL + migrações, Docker, CI.
- [x] **Fase 1 – Núcleo do Server (0.0.2):** usuários e perfis (login, 2FA), matrícula de agentes por token, canal gRPC/TLS, API REST.
- [x] **Fase 2 – Agente e arquivos (0.0.3):** cadastro/status de clients, jobs de pastas (tar+gzip), agendamento com fuso, batimento, fila local.
- [x] **Fase 3 – Conectores de nuvem (0.0.4):** interface `Provider`, OneDrive, Google Drive, verificação por hash, retenção segura.
- [x] **Fase 4 – Bancos de dados (0.0.5):** MySQL/MariaDB, PostgreSQL, MongoDB; teste de restauração automatizado.
- [x] **Fase 5 – Interface completa (0.0.6):** servidores, jobs, histórico, logs ao vivo, temas, logo personalizada, restauração.
- [x] **Fase 6 – Notificações e relatórios (0.0.7):** e-mail, Telegram, PDF A4, configurados pela interface.
- [x] **Fase 7 – Endurecimento e publicação (0.0.8):** TLS, auditoria, limites, documentação, imagens no GitHub Packages.
- [ ] **Fase 8 – Piloto:** instalação em ambiente real, só com autorização e em paralelo ao backup existente.
