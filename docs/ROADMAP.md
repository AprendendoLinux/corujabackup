# Roadmap

- [x] **Fase 0 – Fundação (0.0.1):** repositório, Server/Agente Go, UI React com temas, MySQL + migrações, Docker, CI.
- [x] **Fase 1 – Núcleo do Server (0.0.2):** usuários e perfis (login, 2FA), matrícula de agentes por token, canal gRPC/TLS, API REST.
- [x] **Fase 2 – Agente e arquivos (0.0.3):** cadastro/status de clients, jobs de pastas (tar+gzip), agendamento com fuso, batimento, fila local.
- [x] **Fase 3 – Conectores de nuvem (0.0.4):** interface `Provider`, OneDrive, Google Drive, verificação por hash, retenção segura.
- [x] **Fase 4 – Bancos de dados (0.0.5):** MySQL/MariaDB, PostgreSQL, MongoDB; teste de restauração automatizado.
- [x] **Fase 5 – Interface completa (0.0.6):** servidores, jobs, histórico, logs ao vivo, temas, logo personalizada, restauração.
- [x] **Fase 6 – Notificações e relatórios (0.0.7):** e-mail, Telegram, PDF A4, configurados pela interface.
- [x] **Fase 7 – Endurecimento e publicação (0.0.8):** TLS, auditoria, limites, documentação, imagens no GitHub Packages.
- [ ] **Fase 8 – Pacotes de temas (0.0.9):** temas importáveis (`.zip`) aplicados à interface, ao e-mail e ao PDF; `docs/TEMAS.md`; pacotes de exemplo.
- [ ] **Fase 9 – Destinos de armazenamento (0.0.10):** vários destinos por job; cartões de provedores com login em popup (OAuth); Dropbox, S3 (e compatíveis), SFTP, SMB, SharePoint, pasta local; NFS/iSCSI via volume montado no host.
- [ ] **Fase 10 – Full/Incremental e MikroTik (0.0.11):** motor `tar.gz` em blocos com índice e catálogo; agenda full + incrementais; retenção por cadeia; jobs de MikroTik (SSH: `.rsc` e `.backup`).
- [ ] **Fase 11 – Restauração inteligente (0.0.12):** árvore de arquivos, linha do tempo de versões, restauração de arquivos selecionados e de bancos pela interface.
- [ ] **Fase 12 – Piloto:** instalação em ambiente real, só com autorização e em paralelo ao backup existente; validação com contas reais de nuvem e Telegram.

Decisões: formato próprio (compatível com `tar -xzf`) em vez de restic; NFS/iSCSI por volume do host (perfil privilegiado opcional); aplicativos OAuth registrados pelo mantenedor (pessoa física).
