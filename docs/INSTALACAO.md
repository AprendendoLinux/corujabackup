# Instalação

## Requisitos
- Docker 24+ com Compose v2 no servidor do **Server** e nos servidores dos **agentes**.
- O Server precisa de um MySQL 8.x (o compose já traz um). Os agentes só precisam alcançar o Server na porta `8443`.
- Arquiteturas: `amd64` e `arm64`.

## 1. Server

```bash
curl -fsSLO https://raw.githubusercontent.com/AprendendoLinux/corujabackup/main/deploy/docker-compose.server.yml
# edite o arquivo: troque COLOQUE_UMA_SENHA_FORTE (nos dois lugares)
docker compose -f docker-compose.server.yml up -d
docker logs corujabackup-server 2>&1 | grep "CONFIGURAÇÃO INICIAL"
```

O compose usa a rede Docker externa `echelon`; crie-a (`docker network create echelon`) ou troque o nome no arquivo.

1. Abra `http://IP_DO_SERVER:8080`.
2. Informe o **código de configuração** (do log acima) e crie o administrador. O código existe só enquanto não há usuários e muda a cada início;
   ele impede que um estranho crie o administrador antes de você.
3. Em **Minha conta**, ative o **2FA** e, em **Segurança**, considere exigi-lo dos administradores.

### Portas
| Porta | Para quê | Quem deve alcançar |
| :--- | :--- | :--- |
| `8080` | Interface web e API | Administradores (de preferência atrás de HTTPS) |
| `8443` | Canal gRPC/TLS dos agentes | Os servidores com agente |

### HTTPS
Escolha **uma** opção:

**A) Proxy reverso (recomendado).** Aponte o proxy para `corujabackup-server:8080` e defina `CORUJA_TRUST_PROXY: "true"` no Server (ativa HSTS e cookie `Secure`).
O proxy precisa **acrescentar** o IP do cliente a `X-Forwarded-For` e enviar `X-Forwarded-Proto: https`. Exemplo (nginx):

```nginx
location / {
    proxy_pass http://corujabackup-server:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;              # necessário para o log ao vivo (SSE)
    proxy_read_timeout 1h;
}
```
Não publique o canal dos agentes (`8443`) pelo proxy HTTP: ele tem TLS próprio e é fixado por impressão digital.

**B) HTTPS direto no Server.** Monte um certificado e uma chave PEM e defina `CORUJA_TLS_CERT` e `CORUJA_TLS_KEY`.

### Banco em outro servidor
Defina `CORUJA_DB_HOST`, `CORUJA_DB_NAME`, `CORUJA_DB_USER`, `CORUJA_DB_PASSWORD` e, se não estiver na mesma rede Docker, `CORUJA_DB_TLS=true` (ou `skip-verify`).

## 2. Agente
Na interface: **Agentes > Novo agente** gera o comando pronto (com o token de uso único). Em resumo:

```bash
docker run -d --name corujabackup-agent --restart always \
  -e CORUJA_SERVER=IP_DO_SERVER:8443 -e CORUJA_TOKEN=cb1.... -e CORUJA_HOST_ROOT=/host \
  -v coruja-agent-data:/var/lib/coruja-agent -v /:/host:ro -v /srv/coruja-backups:/backups \
  ghcr.io/aprendendolinux/corujabackup-agent:latest
```

- `/host` (somente leitura) é o que o agente pode salvar; `/backups` é onde grava. Os caminhos dos jobs são os do **servidor** (ex.: `/etc`).
- **Docker socket** (`-v /var/run/docker.sock:/var/run/docker.sock`): só para jobs de banco em contêineres. Dá ao agente controle total do Docker do servidor.
- **Restrinja o agente** (recomendado): `CORUJA_ALLOWED_PATHS=/etc,/srv,/home` e `CORUJA_ALLOWED_CONTAINERS=db-*,mongo`.
- O arquivo `deploy/docker-compose.agent.yml` traz tudo isso, com `cap_drop: ALL` e só as capacidades necessárias.

## 3. Primeiros passos
1. **Nuvem** (opcional): no **Google Drive** das imagens oficiais não há nada a registrar: escolha o provedor, abra o link e digite o código (o aplicativo padrão do Coruja só acessa os arquivos que ele mesmo cria). Para OneDrive, ou para usar o seu próprio aplicativo do Google, registre um aplicativo no provedor e informe o Client ID (a tela explica o passo a passo).
2. **Jobs**: pastas/arquivos ou banco de dados; escolha a agenda, as cópias locais e o destino de nuvem.
3. **Notificações**: crie um canal (e-mail/Telegram), clique em **Enviar teste** e use **Criar regras recomendadas**.
4. **Aparência**: nome, logo e tema da sua marca.

## Referência de variáveis

### Server
| Variável | Padrão | Descrição |
| :--- | :--- | :--- |
| `CORUJA_DB_PASSWORD` | — (**obrigatória**) | Senha do MySQL |
| `CORUJA_DB_HOST` / `PORT` / `NAME` / `USER` | `db` / `3306` / `corujabackup` / `corujabackup` | Conexão com o MySQL |
| `CORUJA_DB_TLS` | vazio | `true`, `skip-verify` ou `preferred` (TLS com o MySQL) |
| `CORUJA_LISTEN` | `:8080` | Interface web/API |
| `CORUJA_AGENT_LISTEN` | `:8443` | Canal dos agentes |
| `CORUJA_DATA_DIR` | `/data` | Certificado do canal dos agentes e chave de criptografia |
| `CORUJA_TRUST_PROXY` | `false` | Confiar em `X-Forwarded-*` (só atrás de proxy confiável) |
| `CORUJA_TLS_CERT` / `CORUJA_TLS_KEY` | vazio | HTTPS direto na interface |

### Agente
| Variável | Padrão | Descrição |
| :--- | :--- | :--- |
| `CORUJA_SERVER` | — | `IP:8443` do Server |
| `CORUJA_TOKEN` | — | Token de matrícula (uso único; depois o agente guarda a própria credencial) |
| `CORUJA_HOST_ROOT` | `/` | Onde o servidor está montado dentro do contêiner |
| `CORUJA_BACKUP_DIR` | `/backups` | Onde os arquivos de backup são gravados |
| `CORUJA_DATA_DIR` | `/var/lib/coruja-agent` | Credencial, agenda local e fila de resultados |
| `CORUJA_ALLOWED_PATHS` | vazio (tudo) | Pastas permitidas (lista separada por vírgula) |
| `CORUJA_ALLOWED_CONTAINERS` | vazio (todos) | Contêineres de banco permitidos (padrões `*` e `?`) |
| `CORUJA_DOCKER_SOCKET` | `/var/run/docker.sock` | Caminho do socket do Docker |
