# 🦉 Coruja Backup

Sistema de backup **centralizado**, em Docker, gerenciado por **interface web**: um **Server** que orquestra tudo e
**Agentes** leves nos servidores de destino. Fase atual: **0.0.1 (fundação)**; o projeto ainda não faz backups.

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

Única variável obrigatória: `CORUJA_DB_PASSWORD`. Opcionais: `CORUJA_DB_HOST` (padrão `db`), `CORUJA_DB_NAME`,
`CORUJA_DB_USER`, `CORUJA_DB_PORT`, `CORUJA_LISTEN` (padrão `:8080`). Todo o resto é configurado na interface.

## Subir um Agente

```bash
cd deploy
# informe CORUJA_SERVER (IP:porta) e CORUJA_TOKEN (gerado na interface, a partir da Fase 1)
docker compose -f docker-compose.agent.yml up -d
```

O acesso ao `docker.sock` (para dumps dentro de contêineres de banco) é **opcional** e vem desligado.

## Desenvolvimento

```bash
cp docker-compose.dev.example.yml docker-compose.dev.yml   # ajuste a senha (arquivo ignorado pelo git)
docker compose -f docker-compose.dev.yml up -d --build      # http://localhost:8081
# testes Go (com um MySQL descartável em CORUJA_TEST_DSN para os de integração):
docker run --rm -v "$PWD":/src -w /src golang:1.24-alpine go test ./...
```

Roteiro completo em [docs/ROADMAP.md](docs/ROADMAP.md); decisões em [docs/ARQUITETURA.md](docs/ARQUITETURA.md).

---
Coruja Backup - Versão 0.0.1 - Desenvolvido por [Henrique Fagundes](https://henrique.tec.br) · Licença MIT
