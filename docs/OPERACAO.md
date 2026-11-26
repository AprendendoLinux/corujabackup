# Operação

## O que fazer backup do próprio Server
O Coruja Backup guarda tudo no MySQL e em **um volume** (`/data`). Faça backup dos dois — e guarde o segundo **separado**:

| O quê | Por quê | Como |
| :--- | :--- | :--- |
| **Banco MySQL** (`corujabackup`) | usuários, jobs, histórico, regras | `docker exec corujabackup-db sh -c 'mysqldump -uroot --single-transaction corujabackup' > coruja.sql` (ou o próprio Coruja Backup, num job de banco!) |
| **Volume `/data`** | `secret.key` (chave que cifra os tokens de nuvem, senhas SMTP/Telegram/bancos) e o certificado do canal dos agentes | copie `secrets/secret.key` e `tls/` para um local seguro |

> ⚠️ **Sem o `secret.key` os dados cifrados do banco ficam ilegíveis** (nuvens, senhas de canais e de bancos precisariam ser recadastradas).
> Sem o certificado em `tls/`, os agentes recusam o Server (a impressão digital muda) e precisam ser matriculados de novo.
> O Server avisa no log, ao iniciar, se a chave atual não abre o que está guardado.

### Recuperação de desastre
1. Suba o MySQL e restaure o dump. 2. Restaure o volume `/data` (ou ao menos `secret.key` e `tls/`). 3. Suba o Server (mesma versão ou mais nova).
Os agentes reconectam sozinhos e entregam o que ficou na fila local.

## Aplicativo OAuth padrão (mantenedores e forks)

As imagens oficiais embutem o aplicativo do Google do projeto: o ID do cliente é público (`internal/cloud/defaults.go`) e o **segredo é injetado só na compilação**, a partir do segredo `GOOGLE_CLIENT_SECRET` do GitHub Actions (ou `GOOGLE_CLIENT_SECRET_FILE` no `scripts/release.sh`). Esse segredo é o de um aplicativo do tipo "TVs e dispositivos de entrada limitada", que o Google documenta como não confidencial, mas ele **fica visível no binário**. Compilações sem o segredo (desenvolvimento, forks) simplesmente não oferecem o modo padrão e pedem o Client ID, como antes. Contas conectadas no modo padrão não gravam credenciais; se o segredo mudar, basta atualizar a imagem.

Enquanto a marca do aplicativo não estiver verificada pelo Google, o app aceita até 100 usuários no total.

## Migração para a 0.0.17 (o Server conecta nos agentes)
Os agentes já matriculados **não precisam de token novo**: a credencial e o certificado fixado continuam valendo.
1. Atualize o Server. A porta dos agentes no compose do Server deixa de ser usada (pode remover o `8443`).
2. Em cada agente: imagem `0.0.17` ou mais nova, troque `CORUJA_SERVER` por `ports: ["23847:23847"]` e recrie o contêiner **mantendo o volume de dados**.
3. Na interface, em **Agentes**, use o ícone de endereço para informar o **IP e a porta** de cada agente. O Server conecta em poucos segundos.

## Atualização
```bash
docker compose -f docker-compose.server.yml pull && docker compose -f docker-compose.server.yml up -d
```
- Faça o backup do banco antes. As migrações são automáticas e **só avançam**: o Server recusa iniciar com um banco migrado por versão **mais nova** (para voltar, restaure o dump).
- Atualize o Server **antes** dos agentes. Agentes antigos continuam funcionando com Server mais novo dentro da mesma série `0.0.x`; confira o [CHANGELOG](../CHANGELOG.md).

## Recuperação de acesso (perdi a senha ou o autenticador)
Com acesso ao servidor onde o Server roda:
```bash
docker exec -it corujabackup-server coruja-server admin reset-password -user henrique -disable-2fa
docker exec -it corujabackup-server coruja-server admin create-admin -user novo.admin
```
A nova senha é lida do terminal (ou de `CORUJA_NEW_PASSWORD`). Todas as sessões do usuário são encerradas e a ação vai para a auditoria.

## Monitoramento
- **Métricas Prometheus**: em **Segurança**, gere o token e use o trecho de `prometheus.yml` mostrado. Principais séries:
  `coruja_agents{state}`, `coruja_job_last_run_ok{job,agent,status}`, `coruja_job_last_run_timestamp_seconds`, `coruja_runs_24h{status}`,
  `coruja_cloud_quota_bytes`, `coruja_cloud_up`, `coruja_notifications{status}`. Alerta sugerido: `time() - coruja_job_last_run_timestamp_seconds > 90000`.
- **Saúde**: `GET /api/health` (usado pelo `HEALTHCHECK` da imagem).
- **Logs**: `docker logs corujabackup-server` / `corujabackup-agent` (texto estruturado; nunca contêm senhas ou tokens).
- **Auditoria**: página Auditoria (exporta CSV). Histórico de execuções: 400 dias; envios de notificação: 90 dias.

## Dimensionamento
- Server: ~100 MB de RAM; o MySQL é leve (centenas de jobs). O envio à nuvem **não** passa pelo Server.
- Agente: ~30 MB de RAM; um backup por vez por agente. Espaço local: o tamanho de uma cópia + o que for mantido (`cópias locais`); use `0` com nuvem para apagar após o envio.

## Solução de problemas
| Sintoma | Causa provável | O que fazer |
| :--- | :--- | :--- |
| Agente "offline" com **tempo esgotado** | firewall bloqueando a porta do agente, IP errado ou máquina desligada | libere a porta do agente para o IP do Server; confira o endereço em **Agentes** (ícone de endereço) |
| Agente "offline" com **conexão recusada** | o agente não está rodando ou a porta não foi publicada na stack | `docker ps`, `docker logs corujabackup-agent`; confira o `ports:` da stack |
| "o agente aceitou a conexão, mas não concluiu a autenticação" | token errado ou já usado, ou o certificado do Server mudou (volume `/data` recriado) | gere um novo agente na interface e use o token novo; se o Server foi reinstalado, apague o volume do agente e matricule de novo |
| "o certificado do Server não confere" | o volume `/data` do Server foi recriado | restaure `tls/` ou matricule os agentes de novo |
| Backup "Perdido" | agente parado/offline no horário | veja o log do agente; o agente recupera horários perdidos em até 6 h |
| "o envio para a nuvem falhou" | token revogado, cota cheia, rede | página Nuvem (Testar conexão); depois **Reenviar** no detalhe da execução |
| Nuvem em "Erro" | refresh token revogado/expirado | Nuvem > Reautorizar |
| Dump de banco falha no modo contêiner | `docker.sock` não montado ou contêiner fora da lista permitida | monte o socket; revise `CORUJA_ALLOWED_CONTAINERS` |
| E-mail não sai | SMTP/segurança/porta | Notificações > Enviar teste mostra o erro exato; histórico de envios traz as tentativas |
| Log ao vivo não aparece atrás de proxy | buffering do proxy | `proxy_buffering off` (veja a instalação) |
| Server não inicia: "banco ... MAIS NOVA" | binário mais antigo que o banco | use a versão correta ou restaure o dump |

## Pastas do agente fora do backup
Os sistemas de arquivos virtuais (`/proc`, `/sys`, `/dev` e semelhantes) entram como pastas vazias, para que o `/` inteiro possa ser selecionado.

Na pasta escolhida para o backup local, que pode ter outros dados, ficam fora **somente os arquivos que o agente gerou**; o restante dela continua sendo salvo.

O que é do próprio agente nunca entra na origem de um backup: a pasta de backups (`/backups`), a raiz de destinos locais (`/destinos`), a pasta de dados (`/var/lib/coruja-agent`, com a credencial e as senhas dos jobs) e o que estiver montado dentro delas. Assim, salvar `/srv` com os backups em `/srv/coruja-backups` não acumula cópias dentro de cópias. A execução registra no log cada pasta deixada de fora. Para guardar uma segunda cópia dos backups, use um **destino** no job, em vez de selecionar essas pastas como origem.

> Monte em `/destinos` somente o que for destino: o que estiver montado ali fica fora da origem, mesmo que tenha outros arquivos.
