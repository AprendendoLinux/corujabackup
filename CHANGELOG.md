# Changelog

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
