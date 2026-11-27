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
- [x] **Fase 9, parte 1 – Nuvens com um clique (0.0.10):** login por popup no Google Drive, OneDrive e Dropbox (conector novo), tela de escolha de destino com ícones; os três validados com contas reais.
- [x] **Fase 9, parte 2 – Destinos de rede (0.0.12):** SFTP, FTP/FTPS, SMB e pasta local/volume do host (NFS e iSCSI).
- [x] **Usabilidade (0.0.13):** seleção de pastas e arquivos por clique (origem, exclusões e destinos), endereço público do canal dos agentes e botões de copiar funcionando em `http://IP`.
- [x] **Pasta local pelo painel (0.0.14):** servidor inteiro visível, pasta do backup local escolhida na interface com o espaço livre, e o que é do agente (backups, destinos e credenciais) fora dos backups.
- [x] **Clareza da interface (0.0.15):** a máquina do backup é sempre chamada de agente; pastas, arquivos, atalhos e padrões identificados nas listas.
- [x] **Interface nova (0.0.16):** menu lateral, ícones, modais próprios, assistentes de job e agente, execução ao vivo, temas só por pacote com modo claro e escuro, créditos configuráveis e foto do usuário.
- [x] **Conexão invertida (0.0.17):** o Server conecta nos agentes (cadastro com IP e porta; porta padrão 23847); a stack do agente só publica a porta e leva o token.
- [x] **Agenda por cliques e partições (0.0.18):** agenda sem cron, partições com uso na escolha da pasta local, retenção em opções e Storages.
- [x] **NFS, pasta local livre e gravação direta (0.0.19):** NFSv3 por cliente embutido no agente (sem montar nada), pasta local escolhida no navegador de pastas, opção de gravar o backup direto no storage (sem arquivo no agente) e nome e sobrenome dos usuários.
- [ ] **Fase 9, parte 3:** vários destinos por job; compatíveis com S3; SharePoint.
- [ ] **Fase 10 – Full/Incremental e MikroTik (versão seguinte):** motor `tar.gz` em blocos com índice e catálogo; agenda full + incrementais; retenção por cadeia; jobs de MikroTik (SSH: `.rsc` e `.backup`).
- [ ] **Fase 11 – Restauração inteligente (versão posterior):** árvore de arquivos, linha do tempo de versões, restauração de arquivos selecionados e de bancos pela interface.
- [ ] **Fase 12 – Piloto:** instalação em ambiente real, só com autorização e em paralelo ao backup existente; validação com contas reais de nuvem e Telegram.

Decisões: formato próprio (compatível com `tar -xzf`) em vez de restic; NFS por cliente NFSv3 embutido no agente (sem privilégios) e iSCSI como "pasta local" num disco já montado no host; aplicativos OAuth registrados pelo mantenedor (pessoa física).
