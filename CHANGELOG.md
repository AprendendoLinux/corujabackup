# Changelog

## [Não publicado] - Fase 9
- **Dropbox:** novo destino, com login por popup (cliente público, sem segredo) e acesso só à pasta do aplicativo. Envio em partes com retomada, conferência pelo `content_hash` do Dropbox, retenção, download e restauração. Como o Dropbox não tem endereço pré-autorizado para arquivos grandes, o agente recebe um **token de curta duração só de escrita de conteúdo** (exceção à regra "o agente não recebe tokens"); cada envio precisa terminar antes de o token vencer (cerca de 4 horas).
- **Correção (todas as nuvens):** quando o agente detectava que o arquivo na nuvem não conferia, ele desistia sem avisar o Server e o arquivo corrompido ficava com nome de backup. Agora o Server confere de novo e o apaga, preservando as cópias boas.
- **Login por popup** para Google Drive, OneDrive e Dropbox (e "Reautorizar" usa o popup nessas contas); tela de escolha de destino com ícones.
- **Aplicativo OAuth padrão do Google Drive** nas imagens oficiais: o usuário só abre o link e digita o código, sem registrar aplicativo (o modo manual continua disponível).

## [0.0.9] - Fase 8 · Pacotes de temas
- **Pacotes de tema (`.zip`):** importados em Aparência; o tema vale para a **interface**, os **e-mails** e os **PDFs** (cores, logo de interface e logo de documento). Importar o mesmo `id` atualiza o tema.
- **Validação rígida:** `.zip` plano e limitado, nomes e tipos verificados, imagens identificadas pelo conteúdo, SVG com script/referência externa recusado, sem CSS livre; auditoria de importação.
- **Cores próprias de e-mail/PDF** (`document`) e logo própria de documento por tema; metadados de pacote não podem ser forjados pelo cliente.
- **CLI:** `coruja-server theme validate|pack` (sem banco de dados).
- **Pacotes incluídos** em `themes/`: `exemplo`, `melloait` e `umami`. Documentação completa em `docs/TEMAS.md` e JSON Schema em `docs/theme.schema.json`.
- **Build das imagens sem emulação:** os estágios de build rodam na plataforma nativa e só o binário Go é cruzado (o workflow de release travava no arm64 sob QEMU).
- **Roadmap revisado** (fases 9 a 12): destinos de armazenamento, full/incremental + MikroTik, restauração inteligente e piloto.

## [0.0.8] - Fase 7 · Endurecimento e publicação
- **Dependências:** gRPC atualizado de 1.72 para 1.85 e projeto migrado para **Go 1.26** — o `govulncheck` apontava 8 vulnerabilidades
  alcançáveis no gRPC (incluindo bypass de autorização e falhas no servidor HTTP/2); agora **zero**. CI roda `govulncheck` e `npm audit`.
- **Configuração inicial protegida:** o administrador só é criado com um **código mostrado nos logs** do Server (muda a cada início),
  com limite de tentativas — ninguém "rouba" o primeiro acesso.
- **Segurança de acesso:** página **Segurança** (política de **exigir 2FA** para ninguém/administradores/todos, duração da sessão),
  **sessões ativas** em Minha conta (listar, encerrar uma ou todas as outras), IP do cliente correto atrás de proxy (vale o último
  `X-Forwarded-For`, não o forjado), cabeçalhos extras (`Permissions-Policy`, COOP/CORP, HSTS em HTTPS, CSP mais restrita),
  timeouts e limites no servidor HTTP, tetos de transmissões ao vivo, **HTTPS direto** (`CORUJA_TLS_CERT/KEY`) e **TLS com o MySQL** (`CORUJA_DB_TLS`).
- **Agente com política local:** `CORUJA_ALLOWED_PATHS` e `CORUJA_ALLOWED_CONTAINERS` limitam o que o agente aceita fazer, mesmo se o Server pedir outra coisa.
- **Operação:** `coruja-server admin reset-password|create-admin` (recuperação de acesso), **métricas Prometheus** em `/metrics` com token,
  exportação da **auditoria em CSV** (com neutralização de fórmulas), limpeza periódica (auditoria 400 dias, limitadores de tentativas),
  aviso no início se a chave de criptografia não abre os dados guardados e **recusa de iniciar com banco migrado por versão mais nova**.
- **Imagens e compose endurecidos:** Server `read_only`, `cap_drop: ALL`, `no-new-privileges`, sem root; agente com `cap_drop: ALL` +
  `DAC_READ_SEARCH`/`DAC_OVERRIDE`. Publicação **multi-arquitetura** (amd64/arm64) automatizada por GitHub Actions, com SBOM e proveniência.
- **Documentação:** README reescrito, Instalação, Operação, Segurança (modelo de ameaças e checklist), FAQ, SECURITY.md e CONTRIBUTING.md.

## [0.0.7] - Fase 6 · Notificações e relatórios
- **Canais configurados pela interface:** **e-mail (SMTP)** com STARTTLS, SSL/TLS direto ou sem criptografia (a senha nunca vai sem TLS
  a menos que você escolha "sem criptografia"; certificado autoassinado é opção explícita) e **Telegram** (bot + vários chats, com
  reenvio como texto simples se o HTML for recusado). Credenciais cifradas no banco e nunca devolvidas pela API; erros não vazam
  senha nem token. Botão **Enviar teste** manda um relatório de exemplo com PDF.
- **Regras:** falha/backup perdido, com avisos, sucesso (por job ou todos), **relatório diário consolidado** (horário e fuso
  próprios, PDF anexo opcional, "só se houver problemas") e **agente offline** (um alerta por queda). Regras recomendadas com um clique.
- **Relatório A4 em PDF** gerado pelo próprio Server (sem Chromium): primeira página = resumo executivo (estado geral, contadores,
  atenção, jobs, nuvem com cota, agentes); demais páginas = execuções do período. Usa nome, **logo** e cores escolhidos em Aparência.
  E-mail em HTML (tabelas + CSS inline, logo embutida) com versão em texto; mensagem de Telegram dentro do limite de 4096 caracteres.
- **Fila com novas tentativas:** cada envio é registrado (histórico na interface), com até 5 tentativas (1, 5, 15 e 30 min) e sem
  duplicar (chave única por regra/canal/evento). Entrega parcial no Telegram não repete para quem já recebeu.
- Nomes de jobs, nuvens, canais e regras aceitam acentos ("Relatório diário"); o nome do arquivo de backup é gerado sem acentos.

## [0.0.6] - Fase 5 · Interface completa
- **Logs ao vivo:** o agente envia as linhas do log a cada 2 s durante o backup e o Histórico as mostra em tempo real (SSE,
  com selo "ao vivo", rolagem automática e barra de progresso); cai para atualização comum se a transmissão falhar.
- **Identidade visual (white-label):** página **Aparência** (administradores) para trocar o **nome** do sistema, enviar uma
  **logo personalizada** (PNG, JPEG, GIF, WebP ou SVG sem scripts, até 1 MiB; o tipo é decidido pelo conteúdo e servida
  isolada, com CSP `sandbox`), escolher o **tema padrão** e criar até 10 **temas personalizados** com seletor de cores e
  pré-visualização imediata. Nome e logo aparecem no topo, no login e na aba do navegador; o rodapé padrão é mantido.
- **Restauração:** no detalhe de cada execução, **baixar da nuvem** (o Server só repassa o fluxo, sem gravar em disco; o token
  da nuvem nunca é enviado a endereços de redirecionamento), **reenviar para a nuvem** quando o envio falhou (reaproveita o
  arquivo local preservado e corrige o estado da execução), **extrair no agente** para `restores/<pasta>` (extração segura:
  bloqueia `..`, caminhos absolutos e escrita através de links; nunca sobrescreve) e **guia de restauração manual** com os
  comandos certos para arquivos, MySQL/MariaDB, PostgreSQL e MongoDB. Todas as ações ficam na auditoria e no log da execução.

## [0.0.5] - Fase 4 · Bancos de dados
- **Jobs de banco de dados** para **MySQL, MariaDB, PostgreSQL e MongoDB**, configurados na interface (escolha do tipo ao criar o job).
- **Dois modos:** *contêiner Docker* (o dump roda **dentro** do contêiner, com as ferramentas da versão certa, pela Docker Engine API;
  sem usuário/senha informados, usa as credenciais do próprio contêiner) ou *rede* (host e porta, com os clientes incluídos no agente).
  A interface busca os contêineres do agente e tem "Testar conexão" (lista os bancos e a versão).
- **Um arquivo por execução:** `.tar.gz` com um dump por banco + `manifest.json` (versão, tamanhos, SHA-256, nº de tabelas),
  seguindo o mesmo fluxo de verificação, envio à nuvem (conferido por hash), retenção local e na nuvem dos jobs de arquivos.
- **Conferência real de cada dump:** MySQL/MariaDB pelo marcador de conclusão (não confia só no código de saída), PostgreSQL com
  `pg_restore --list`, MongoDB pela assinatura e integridade do arquivo; PostgreSQL também salva papéis/globais.
- **Teste de restauração automatizado (opcional, recomendado):** restaura o backup num **servidor descartável** (contêiner sem rede,
  apagado depois), compara o número de tabelas/coleções com a origem e registra o resultado; falha vira aviso destacado.
- Segredos: a senha do banco fica cifrada no Server, nunca volta pela API, e vai ao agente só pelo canal TLS; os comandos recebem
  credenciais por variáveis de ambiente, não por argumentos.
- Testado com contêineres **reais** (MySQL 8.0, MariaDB 11.4, PostgreSQL 17, MongoDB 7.0), inclusive o fluxo completo até a nuvem.

## [0.0.4] - Fase 3 · Conectores de nuvem
- **OneDrive (Microsoft Graph) e Google Drive (Drive v3)**, arquitetura modular (`Provider`): conta, cota, sessão de upload,
  consulta de hashes, listagem e exclusão **definitiva** (sem lixeira, que continuaria ocupando a cota).
- **Autorização por código de dispositivo** (sem URL de retorno), na própria interface; tokens renovados automaticamente
  e **cifrados no banco** (AES-256-GCM, chave no volume `/data`). Google com escopo mínimo `drive.file`.
- **O agente envia direto para a nuvem** por uma sessão retomável emitida pelo Server: o agente nunca recebe tokens.
  Blocos de 10 MiB (OneDrive) / 8 MiB (Drive), retomada após falhas de rede e renovação de sessão expirada.
- **Conferência obrigatória:** o agente compara os hashes que a nuvem devolve (quickXorHash, SHA-1 ou MD5) e o Server
  confere de novo por conta própria; se não bater, o arquivo ruim é removido da nuvem e os backups antigos são preservados.
- **Retenção na nuvem** (por quantidade e/ou dias) só depois do envio verificado; o arquivo mais novo nunca é apagado.
  Cópia local configurável (0 = apagar após a nuvem confirmar; se o envio falhar, o arquivo local é preservado).
- Recusa o envio se a cota da conta não comporta o arquivo. Fases e progresso do envio aparecem no Histórico.
- Execuções órfãs (agente reiniciado no meio do backup) são marcadas como falha ao reconectar.
- Interface: página **Nuvem** (conectar, testar, reautorizar, cota) e seção de nuvem no formulário do job.

## [0.0.3] - Fase 2 · Agente e backups de arquivos
- **Jobs de arquivos** cadastrados na interface: pastas/arquivos, exclusões (`*.tmp`, `*/data/cache/*`), agenda cron com
  fuso horário, pasta de destino e quantidade de cópias a manter.
- **O agente agenda sozinho:** o Server envia as definições e o agente dispara no horário, mesmo com o Server fora do ar.
  Se ficou parado, recupera o último horário perdido (janela de 6 h). Um backup por vez; o mesmo job nunca roda em paralelo.
- **Execução:** `.tar.gz` com nome datado, escrito em arquivo temporário e renomeado só no final; **verificação** (SHA-256 +
  gzip + contagem de itens) antes de concluir; **retenção** só depois de verificado e só sobre arquivos do próprio job.
  Pasta de destino confinada à área de backups do agente. Cancelamento não deixa arquivo parcial.
- **Fila local de resultados:** o resultado (com o log completo) fica no disco do agente até o Server confirmar o recebimento
  (idempotente por UUID). Horários que o agente não cumpriu aparecem como **Perdido** no histórico.
- **Interface:** páginas **Jobs** (criar/editar/pausar/executar agora/excluir) e **Histórico** (filtros, detalhe com log,
  cancelar), estatísticas das últimas 24 h no Painel.

## [0.0.2] - Fase 1 · Núcleo do Server
- **Login e perfis:** configuração inicial (primeiro administrador), login com senha (argon2id), sessões por cookie
  `HttpOnly`/`SameSite=Strict`, perfis Administrador, Operador e Somente leitura, gestão de usuários, troca de senha.
- **2FA (TOTP, RFC 6238):** QR code, anti-replay, 8 códigos de recuperação; reset pelo administrador.
- **Proteções:** limite de tentativas de login (por IP e por usuário), erro genérico (sem enumeração de usuários),
  proteção CSRF (cabeçalho + Origin), cabeçalhos de segurança/CSP, trava do último administrador, auditoria.
- **Agentes:** matrícula por **token de uso único** que carrega a impressão digital do certificado do Server (o agente só
  precisa de IP:porta + token e confirma que fala com o Server certo, sem confiar no primeiro uso); credencial própria
  por agente (guardada só como hash), revogação imediata, renomear e excluir.
- **Canal gRPC/TLS 1.3** iniciado pelo agente (porta 8443): Hello, batimentos, estado online/offline, reconexão com espera
  crescente. Certificado autoassinado gerado na primeira execução (volume `/data`).
- **Interface:** telas de configuração, login (com 2FA), Painel, Agentes (gera o comando de instalação), Usuários,
  Auditoria e Minha conta.

## [0.0.1] - Fase 0 · Fundação
- Estrutura do projeto: Server e Agente em Go, interface web em React + TypeScript + Tailwind (embutida no binário do Server).
- Server: API `/api/health`, `/api/version`, `/api/branding`; conexão MySQL com espera e migrações embutidas.
- Interface: tema padrão **Coruja** (logo e paleta), temas Noturno e Floresta, rodapé padrão.
- Agente: verifica a conectividade com o Server (matrícula e jobs chegam nas próximas fases).
- Docker: imagens `server` e `agent` (build multi-stage), compose de produção com `mysql:latest` e rede `echelon`.
