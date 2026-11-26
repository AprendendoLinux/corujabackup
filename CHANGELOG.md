# Changelog

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
