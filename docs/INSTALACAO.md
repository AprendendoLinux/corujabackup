# Instalação

## Requisitos
- Docker 24+ com Compose v2 no servidor do **Server** e nos servidores dos **agentes**.
- O Server precisa de um MySQL 8.x (o compose já traz um). O **Server precisa alcançar cada agente** na porta dele (padrão `23847`).
- Arquiteturas: `amd64` e `arm64`.

## 1. Server

```bash
curl -fsSLO https://raw.githubusercontent.com/AprendendoLinux/corujabackup/main/deploy/docker-compose.server.yml
# edite o arquivo: troque COLOQUE_UMA_SENHA_FORTE (nos dois lugares)
docker compose -f docker-compose.server.yml up -d
```

O compose usa a rede Docker externa `echelon`; crie-a (`docker network create echelon`) ou troque o nome no arquivo.

Os dados ficam em **pastas do servidor**, não em volumes do Docker: `/srv/coruja-server/mysql` (banco) e `/srv/coruja-server/data` (certificado do canal dos agentes e chave de criptografia; faça backup dela). As pastas são criadas na primeira subida; o serviço `permissoes` do compose acerta o dono da pasta do Server (usuário `10001`) e termina.

> **Atualizando uma instalação anterior à 0.0.31** (que usava os volumes `db-data` e `server-data`)? Copie os dados para as pastas antes de subir o compose novo, com tudo parado:
> ```bash
> docker compose -f docker-compose.server.yml down               # ainda com o compose ANTIGO
> docker volume ls | grep -E 'db-data|server-data'               # ex.: coruja_db-data e coruja_server-data
> sudo mkdir -p /srv/coruja-server/mysql /srv/coruja-server/data
> docker run --rm -v coruja_db-data:/de -v /srv/coruja-server/mysql:/para alpine cp -a /de/. /para/
> docker run --rm -v coruja_server-data:/de -v /srv/coruja-server/data:/para alpine cp -a /de/. /para/
> ```
> Depois suba o compose **novo** e confira (login, agentes online). Só então apague os volumes antigos. Sem a pasta `/data` antiga, os agentes não reconhecem o Server e os tokens de nuvem não abrem.

### Primeiro acesso: `admin` / `admin`
1. Abra `http://IP_DO_SERVER:8080` e entre com o usuário **`admin`** e a senha **`admin`**.
2. O sistema pede **uma senha nova** (mínimo de 10 caracteres, diferente da atual) antes de liberar qualquer tela. Até a troca, a API recusa
   todo o resto. A senha padrão deixa de valer na hora.
3. Em **Minha conta**, ajuste o nome, ative o **2FA** e, em **Segurança**, considere exigi-lo dos administradores. Crie usuários nominais
   para as outras pessoas em **Usuários**.

> **Faça o primeiro acesso logo após subir o Server**, de preferência antes de liberar a porta `8080` na rede: enquanto a senha padrão não é
> trocada, qualquer um que alcance a interface pode entrar como `admin`. O usuário padrão só é criado quando o banco não tem nenhum usuário
> (a primeira subida); numa instalação existente, nada muda. Perdeu a senha? `docker exec -it corujabackup-server coruja-server admin reset-password -user admin` (veja docs/OPERACAO.md).

### Portas
| Porta | Para quê | Quem deve alcançar |
| :--- | :--- | :--- |
| `8080` | Interface web e API | Administradores (de preferência atrás de HTTPS) |
| `23847` (na máquina de cada **agente**) | Canal gRPC/TLS: o Server conecta no agente | Só o IP do Server |

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
O canal dos agentes não passa pelo proxy HTTP: o Server conecta direto em cada agente, com TLS próprio fixado por impressão digital.

**B) HTTPS direto no Server.** Monte um certificado e uma chave PEM e defina `CORUJA_TLS_CERT` e `CORUJA_TLS_KEY`.

### Banco em outro servidor
Defina `CORUJA_DB_HOST`, `CORUJA_DB_NAME`, `CORUJA_DB_USER`, `CORUJA_DB_PASSWORD` e, se não estiver na mesma rede Docker, `CORUJA_DB_TLS=true` (ou `skip-verify`).

### Agente da própria máquina do Server
O compose do Server já traz o serviço **`agent`**: o agente da máquina do Server, para ela fazer backup **dela mesma**.
- Ele se **matricula sozinho**: na subida, o Server cria o agente `coruja-server` (nome em `CORUJA_LOCAL_AGENT_NAME`) e grava um token de
  uso único em `/srv/coruja-server/matricula`, pasta que só os dois contêineres montam. O agente lê o token, o Server conecta nele pela
  rede interna do Docker (o agente **não publica porta**) e o arquivo é apagado assim que a matrícula termina. Em segundos ele aparece
  **online** em Agentes, sem nada para copiar e colar.
- **O que vale salvar dele:** a pasta `/srv/coruja-server/data` (certificado e chave de criptografia, essenciais para restaurar o Server) e
  o **banco**, com um job de banco de dados em modo **rede**: servidor `corujabackup-db`, porta 3306, usuário `corujabackup` com a senha
  do compose e **ignorar verificação do certificado** ligado (o MySQL sobe com certificado autoassinado). Copiar a pasta do MySQL
  com o banco rodando não é um backup confiável: use o job de banco. Envie os dois para um storage **fora** desta máquina.
- **Não quer o agente local?** Remova o serviço `agent` e a variável `CORUJA_LOCAL_AGENT` do Server.
- **Excluiu o agente `coruja-server` na interface?** A próxima subida do Server gera outro token. Para o agente aceitar, apague também a
  pasta `/srv/coruja-server/agente` (a credencial antiga) e recrie o contêiner do agente.

## 2. Agente
Na interface: **Agentes > Novo agente** pede o nome, o **IP e a porta** da máquina do agente e gera a stack pronta (com o token de uso único). Depois que o agente sobe, o Server conecta nele sozinho. Em resumo:

```bash
docker run -d --name corujabackup-agent --restart always -p 23847:23847 \
  -e CORUJA_TOKEN=cb1.... -e CORUJA_HOST_ROOT=/host \
  -v /srv/coruja-agente:/var/lib/coruja-agent -v /:/host:ro -v /:/host-rw -v /srv/coruja-backups:/backups \
  ghcr.io/aprendendolinux/corujabackup-agent:latest
```

- `/srv/coruja-agente` guarda a credencial do agente, a agenda local e a fila de resultados (o agente a deixa fora dos backups). Numa instalação anterior à 0.0.31, com o volume `coruja-agent-data` (ou `<projeto>_agent-data`), copie o conteúdo dele para a pasta antes de trocar: `docker run --rm -v coruja-agent-data:/de -v /srv/coruja-agente:/para alpine cp -a /de/. /para/`. Sem isso, o agente precisa ser matriculado de novo.
- `/host` (somente leitura) é o que o agente pode salvar: por padrão, o `/` inteiro. Os caminhos dos jobs são os do **servidor** (ex.: `/etc`).
- `/host-rw` permite escolher no painel a **pasta do backup local** em qualquer disco do servidor, vendo o espaço livre. Ele dá ao agente permissão de gravação no servidor (veja [Segurança](SEGURANCA.md)). `/backups` é a pasta padrão do agente.
- **Docker socket** (`-v /var/run/docker.sock:/var/run/docker.sock`): só para jobs de banco em contêineres. Dá ao agente controle total do Docker do servidor.
- **Restrinja o agente** (recomendado): `CORUJA_ALLOWED_PATHS=/etc,/srv,/home` e `CORUJA_ALLOWED_CONTAINERS=db-*,mongo`.
- **Firewall:** libere a porta publicada (`23847` no exemplo) **só para o IP do Server**.
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
| `CORUJA_DATA_DIR` | `/data` | Certificado do canal dos agentes e chave de criptografia |
| `CORUJA_TRUST_PROXY` | `false` | Confiar em `X-Forwarded-*` (só atrás de proxy confiável) |
| `CORUJA_TLS_CERT` / `CORUJA_TLS_KEY` | vazio | HTTPS direto na interface |

### Agente
| Variável | Padrão | Descrição |
| :--- | :--- | :--- |
| `CORUJA_LISTEN` | `:23847` | Porta onde o agente espera a conexão do Server (publique-a na stack) |
| `CORUJA_TOKEN` | — | Token de matrícula (uso único; depois o agente guarda a própria credencial) |
| `CORUJA_HOST_ROOT` | `/` | Onde o servidor está montado dentro do contêiner |
| `CORUJA_BACKUP_DIR` | `/backups` | Onde os arquivos de backup são gravados |
| `CORUJA_DATA_DIR` | `/var/lib/coruja-agent` | Credencial, agenda local e fila de resultados |
| `CORUJA_ALLOWED_PATHS` | vazio (tudo) | Pastas permitidas (lista separada por vírgula) |
| `CORUJA_BACKUP_ROOTS` | vazio (tudo) | Onde o backup local pode ser gravado quando escolhido no painel |
| `CORUJA_HOST_WRITE` | `/host-rw` | Raiz gravável do servidor, como o agente a enxerga |
| `CORUJA_ALLOWED_CONTAINERS` | vazio (todos) | Contêineres de banco permitidos (padrões `*` e `?`) |
| `CORUJA_DOCKER_SOCKET` | `/var/run/docker.sock` | Caminho do socket do Docker |
