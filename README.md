# 🦉 Coruja Backup

Sistema de backup **centralizado**, em Docker, gerenciado por **interface web**: um **Server** que orquestra tudo e
**Agentes** leves nos servidores de destino. Fase atual: **0.0.6 (Fase 5: interface completa)**.

## Como funciona

```
 ┌────────────┐  gRPC/TLS (agente → server)   ┌───────────────────────┐   MySQL   ┌────────┐
 │  Agente    │ ────────────────────────────▶ │  Server (UI + API)    │ ────────▶ │ mysql  │
 │ (cada host)│   IP:porta + token            │  agenda, orquestra    │           └────────┘
 └─────┬──────┘                               └──────────┬────────────┘
       │ upload direto (sessão emitida pelo Server)       │ credenciais da nuvem ficam só aqui
       ▼                                                  ▼
   OneDrive · Google Drive · (outros conectores)
```

- **Server:** guarda configuração, agendamentos, histórico e as credenciais da nuvem; expõe **uma porta**.
- **Agente:** só precisa de **IP:porta + token**. Faz tar das pastas e dumps de bancos (MySQL/MariaDB, PostgreSQL, MongoDB…) e envia direto à nuvem.
- **Conectores de nuvem** modulares (OneDrive e Google Drive primeiro). **White-label:** temas e logo personalizada.

## Subir o Server

```bash
cd deploy
# troque COLOQUE_UMA_SENHA_FORTE nos dois lugares do arquivo
docker compose -f docker-compose.server.yml up -d
# abra http://IP_DO_SERVER:8080
```

Na primeira abertura a interface pede para criar o administrador. Única variável obrigatória: `CORUJA_DB_PASSWORD`. Opcionais: `CORUJA_AGENT_LISTEN` (padrão `:8443`), `CORUJA_DATA_DIR` (padrão `/data`), `CORUJA_TRUST_PROXY`, `CORUJA_DB_HOST` (padrão `db`), `CORUJA_DB_NAME`,
`CORUJA_DB_USER`, `CORUJA_DB_PORT`, `CORUJA_LISTEN` (padrão `:8080`). Todo o resto é configurado na interface.

## Subir um Agente

```bash
cd deploy
# informe CORUJA_SERVER (IP:8443) e CORUJA_TOKEN (Agentes > Novo agente, na interface; o token vale uma vez)
docker compose -f docker-compose.agent.yml up -d
```

O agente lê o servidor por `/host` (somente leitura, `CORUJA_HOST_ROOT=/host`) e grava os backups em `/backups`; os jobs são criados na interface (Jobs). O acesso ao `docker.sock` (para dumps dentro de contêineres de banco) é **opcional** e vem desligado.

## Desenvolvimento

```bash
cp docker-compose.dev.example.yml docker-compose.dev.yml   # ajuste a senha (arquivo ignorado pelo git)
docker compose -f docker-compose.dev.yml up -d --build      # http://localhost:8081
# testes Go (com um MySQL descartável em CORUJA_TEST_DSN para os de integração):
docker run --rm --network SUA_REDE -e CORUJA_TEST_DSN='usuario:senha@tcp(db:3306)/banco_de_teste?parseTime=true&loc=UTC' -v "$PWD":/src -w /src golang:1.24-alpine go test -p 1 ./...
# ATENÇÃO: os testes de integração APAGAM as tabelas do banco indicado em CORUJA_TEST_DSN; use um banco descartável.
./scripts/gen-proto.sh   # regenera internal/pb a partir de proto/ (só se mudar o .proto)
```

Roteiro completo em [docs/ROADMAP.md](docs/ROADMAP.md); decisões em [docs/ARQUITETURA.md](docs/ARQUITETURA.md).

---
Coruja Backup - Versão 0.0.6 - Desenvolvido por [Henrique Fagundes](https://henrique.tec.br) · Licença MIT
