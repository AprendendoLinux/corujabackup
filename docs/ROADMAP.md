# Roadmap

- [x] **Fase 0 – Fundação (0.0.1):** repositório, Server/Agente Go, UI React com temas, MySQL + migrações, Docker, CI.
- [x] **Fase 1 – Núcleo do Server (0.0.2):** usuários e perfis (login, 2FA), matrícula de agentes por token, canal gRPC/TLS, API REST.
- [x] **Fase 2 – Agente e arquivos (0.0.3):** cadastro/status de clients, jobs de pastas (tar+gzip), agendamento com fuso, batimento, fila local.
- [x] **Fase 3 – Conectores de nuvem (0.0.4):** interface `Provider`, OneDrive, Google Drive, verificação por hash, retenção segura.
- [x] **Fase 4 – Bancos de dados (0.0.5):** MySQL/MariaDB, PostgreSQL, MongoDB; teste de restauração automatizado.
- [x] **Fase 5 – Interface completa (0.0.6):** servidores, jobs, histórico, logs ao vivo, temas, logo personalizada, restauração.
- [x] **Fase 6 – Notificações e relatórios (0.0.7):** e-mail, Telegram, PDF A4, configurados pela interface.
- [x] **Fase 7 – Endurecimento e publicação (0.0.8):** TLS, auditoria, limites, documentação, imagens no GitHub Packages.
- [x] **Fase 8 – Pacotes de temas (0.0.9):** temas importáveis (`.zip`) aplicados à interface, ao e-mail e ao PDF; `docs/TEMAS.md`; pacotes de exemplo.
- [ ] **Fase 9 – Destinos de armazenamento (0.0.10):** entregue em etapas, cada uma testável sozinha:
  - **9a · Modelo de destinos:** um job passa a ter uma lista de destinos (pasta local do agente + contas), cada um com retenção e conferência próprias; `Provider` ganha um tipo de sessão por protocolo.
  - **9b · Compatíveis com S3** (AWS, MinIO, Wasabi, Backblaze): URLs pré-assinadas pelo Server, envio em partes (arquivos grandes), credenciais só no Server.
  - **9c · Dropbox e SharePoint:** login por código no navegador (cartões com janela de login); SharePoint como biblioteca de documentos do OneDrive.
  - **9d · Destinos diretos do agente:** SFTP e SMB (clientes Go nativos, credencial cifrada no Server e entregue ao agente), pasta local/volume do host (NFS e iSCSI montados no host).
- [ ] **Fase 10 – Full/Incremental e MikroTik (0.0.11):** motor `tar.gz` em blocos com índice e catálogo; agenda full + incrementais; retenção por cadeia; jobs de MikroTik (SSH: `.rsc` e `.backup`).
- [ ] **Fase 11 – Restauração inteligente (0.0.12):** árvore de arquivos, linha do tempo de versões, restauração de arquivos selecionados e de bancos pela interface.
- [ ] **Fase 12 – Piloto:** instalação em ambiente real, só com autorização e em paralelo ao backup existente; validação com contas reais de nuvem e Telegram.

Decisões: formato próprio (compatível com `tar -xzf`) em vez de restic; NFS/iSCSI por volume do host (perfil privilegiado opcional); aplicativos OAuth registrados pelo mantenedor (pessoa física).
