# Arquitetura e decisões

| Tema | Decisão | Motivo |
| :--- | :--- | :--- |
| Linguagem (Server e Agente) | Go, um módulo só | binário estático (~15 MB), pouca RAM no agente, concorrência nativa, fácil imagem mínima |
| Comunicação | gRPC em stream bidirecional sobre TLS, **iniciado pelo agente** | funciona atrás de NAT/firewall; comandos e progresso tipados; um canal por agente |
| Portas | `8080` (interface + API REST, atrás de proxy reverso/HTTPS) e `8443` (canal gRPC/TLS dos agentes) | o canal dos agentes tem TLS próprio e fixado por impressão digital; a UI pode ficar atrás do proxy |
| Interface | React + TypeScript + Vite + Tailwind, embutida (`go:embed`) | sem contêiner extra; temas = variáveis CSS; logo por upload |
| Banco do Server | MySQL (`mysql:latest`; recomendado fixar `8.4`) | pedido do projeto; `db` no compose de produção, `db-server` no desenvolvimento |
| Caminho dos dados | **agente → nuvem direto**, com sessão de upload emitida pelo Server | credenciais ficam só no Server; o tráfego pesado não passa por ele |
| Nuvem | interface `Provider` por pacote (OneDrive/Graph e Google Drive/Drive v3 primeiro) | extensível; verificação por `quickXorHash` (OneDrive) e `md5` (Drive) |
| Bancos | drivers (`mysqldump`, `pg_dump`, `mongodump`…), também via `docker exec` | mesma interface para qualquer SGBD |
| Docker no agente | `docker.sock` **opcional** (desligado por padrão) | necessário só para entrar em contêineres de banco |
| Distribuição | GitHub (código, releases e imagens em Packages/ghcr.io) | sem Docker Hub neste projeto |

## Regras aprendidas (da versão anterior em Bash)
- A retenção na nuvem só roda **depois** do envio verificado por hash.
- O agente mantém fila local quando o Server está fora e nunca executa o mesmo job em duplicidade.
- Relatório/alerta só dispara dentro da janela configurada, nunca por "ciclo antigo" (evita o falso alerta).
- Segredos nunca em logs; credenciais da nuvem criptografadas no banco (Fase 3).

## Segurança do canal dos agentes (Fase 1)
- O token de matrícula (`cb1.<impressão digital>.<segredo>`) é de **uso único** e expira; o Server guarda só o SHA-256 do segredo.
- O agente fixa (pinning) a impressão digital do certificado do Server, vinda do próprio token. Não há "confiar no primeiro uso".
- Após a matrícula, o agente usa uma credencial própria (`id.segredo`), guardada como hash; revogar derruba o canal na hora.
- Tentativas de matrícula/autenticação são limitadas por IP; o login por IP e por usuário.

## Execução de jobs (Fase 2)
- **Quem agenda:** o Server guarda os jobs e envia o conjunto completo ao agente (`SyncJobs`); o **agente agenda sozinho**
  (cron com fuso) e persiste jobs e "último horário disparado" no disco. Assim o backup roda mesmo com o Server offline.
- **Resultados:** cada execução tem um UUID. O agente grava o relatório final (com o log completo) na fila local e só
  apaga quando recebe `ReportAck`; o Server faz *upsert* por UUID, então reenvios nunca duplicam.
- **Perdidos:** o Server marca como `missed` o horário previsto sem execução (após 15 min de tolerância). Se o agente
  recuperar o atraso depois, a linha é adotada pela execução real.
- **Segurança:** o destino é sempre uma subpasta relativa dentro de `CORUJA_BACKUP_DIR` (o agente recusa `..` e caminhos
  absolutos); as origens são lidas via `CORUJA_HOST_ROOT` (o host montado somente leitura).
- **Destinos:** nesta fase o destino é local (no agente). Os conectores de nuvem (Fase 3) enviam esse mesmo arquivo.

## Nuvem (Fase 3)
- **Credenciais só no Server**, cifradas (AES-256-GCM; chave em `/data/secrets/secret.key`). O agente recebe apenas a URL de uma
  sessão de upload já autenticada, que expira.
- **Fluxo:** agente `UploadRequest` -> Server abre a sessão no provedor -> agente envia os blocos direto à nuvem e confere os
  hashes devolvidos -> `UploadDone` -> o Server consulta a nuvem (Stat), confere e **só então** aplica a retenção -> `UploadVerdict`.
- **Nomes controlados:** o Server só aceita nomes no padrão `<slug-do-job>-AAAAMMDD-HHMMSS.tar.gz` e pastas validadas
  (sem `..`); a retenção só toca arquivos com esse padrão.
- **Verificação:** OneDrive Business: quickXorHash (implementação própria, validada contra o rclone); OneDrive pessoal: SHA-1;
  Google Drive: MD5; sem hash disponível, cai para o tamanho (registrado no histórico).
- **Falhas:** envio falhou => execução `failed`, arquivo local preservado, nuvem intacta. Conta com token revogado fica em
  estado de erro até ser reautorizada.
- **Provedores futuros:** basta implementar `cloud.Provider` e registrar em `cloudmgr`.
