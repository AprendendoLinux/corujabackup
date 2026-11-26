# 🦉 Coruja Backup

Sistema de backup **centralizado**, em Docker, gerenciado por **interface web**. Um **Server** orquestra tudo; **Agentes** leves rodam
nos servidores de destino e se conectam a ele informando só **IP:porta + token**.

> **Versão 0.0.13** · [Changelog](CHANGELOG.md) · [Instalação](docs/INSTALACAO.md) · [Operação](docs/OPERACAO.md) · [Segurança](docs/SEGURANCA.md) · [FAQ](docs/FAQ.md)

## O que faz

| Área | Recursos |
| :--- | :--- |
| **Backups** | Pastas/arquivos (`.tar.gz` com exclusões) e **bancos de dados** (MySQL, MariaDB, PostgreSQL, MongoDB). Agenda cron com fuso; o **agente agenda sozinho** e roda mesmo com o Server fora do ar. |
| **Destinos de rede** | **SFTP**, **FTP/FTPS**, **SMB/CIFS** e **pasta local** (NFS/iSCSI montados no host): o agente envia, confere relendo o arquivo e aplica a retenção. |
| **Confiança** | Cada arquivo é conferido (SHA-256, gzip, estrutura) e há **teste de restauração real** de bancos num servidor descartável. A retenção só roda depois de verificar. Horários perdidos viram alerta. |
| **Nuvem** | **Google Drive**, **OneDrive** e **Dropbox**, conectados com um clique (login por popup): o agente envia **direto** (envio retomável), conferência por hash do agente **e** do Server, retenção por quantidade/dias, exclusão definitiva (sem lixeira). |
| **Restauração** | Baixar da nuvem, reenviar, extrair no agente (extração segura) e guia manual por tipo de backup. |
| **Avisos** | E-mail (SMTP) e Telegram; falha, aviso, sucesso, agente offline e **relatório diário consolidado em PDF A4**. |
| **Interface** | Painel, agentes, jobs, histórico com **log ao vivo**, nuvem, notificações, **white-label** (nome, logo, temas e **pacotes de tema `.zip`** para interface, e-mail e PDF), usuários e perfis, 2FA, auditoria. |
| **Operação** | Métricas Prometheus, CLI de recuperação de acesso, política de 2FA/sessão, imagens endurecidas e multi-arquitetura. |

## Como funciona

```
 ┌────────────┐  gRPC/TLS 1.3 (agente → server)   ┌───────────────────────┐   MySQL   ┌────────┐
 │  Agente    │ ────────────────────────────────▶ │  Server (UI + API)    │ ────────▶ │ mysql  │
 │ (cada host)│   IP:8443 + token                 │  agenda, orquestra    │           └────────┘
 └─────┬──────┘                                   └──────────┬────────────┘
       │ envio direto (sessão emitida pelo Server)            │ credenciais da nuvem ficam só aqui (cifradas)
       ▼                                                      ▼
   Drive · OneDrive · Dropbox                         e-mail · Telegram · Prometheus
```

- O **Server** guarda configuração, histórico e credenciais (cifradas) e expõe duas portas: `8080` (interface/API) e `8443` (agentes).
- O **Agente** só faz conexões de saída, não abre portas, e executa apenas o que a política local permitir.
- Tudo é configurado pela interface; as variáveis de ambiente dos compose são mínimas.

## Instalação rápida

```bash
# Server + MySQL (troque COLOQUE_UMA_SENHA_FORTE nos dois lugares do arquivo)
curl -fsSLO https://raw.githubusercontent.com/AprendendoLinux/corujabackup/main/deploy/docker-compose.server.yml
docker compose -f docker-compose.server.yml up -d
docker logs corujabackup-server 2>&1 | grep "CONFIGURAÇÃO INICIAL"     # código para criar o administrador
# abra http://IP_DO_SERVER:8080, crie o administrador e vá em Agentes > Novo agente
```

O guia completo (HTTPS, proxy reverso, firewall, agentes, nuvem) está em [docs/INSTALACAO.md](docs/INSTALACAO.md).

## Documentação

- [Instalação](docs/INSTALACAO.md) — Server, agentes, HTTPS/proxy, primeiros passos e referência de variáveis.
- [Operação](docs/OPERACAO.md) — backup do próprio Server, atualização, recuperação de acesso, monitoramento, solução de problemas.
- [Segurança](docs/SEGURANCA.md) — modelo de ameaças, o que protege, checklist de endurecimento.
- [Destinos de rede](docs/DESTINOS.md) — SFTP, FTP/FTPS, SMB e pasta local (NFS/iSCSI).
- [Temas](docs/TEMAS.md) — pacotes `.zip` de identidade visual (interface, e-mail e PDF).
- [Arquitetura](docs/ARQUITETURA.md) e [Roadmap](docs/ROADMAP.md) — decisões técnicas e fases.
- [FAQ](docs/FAQ.md) · [Contribuindo](CONTRIBUTING.md) · [Política de segurança](SECURITY.md)

## Desenvolvimento

```bash
cp docker-compose.dev.example.yml docker-compose.dev.yml     # ajuste a senha (arquivo ignorado pelo git)
docker compose -f docker-compose.dev.yml up -d --build        # http://localhost:8081
# testes (Go 1.26 em contêiner; os de integração APAGAM as tabelas do banco indicado: use um banco descartável)
docker run --rm --network SUA_REDE -e CORUJA_TEST_DSN='usuario:senha@tcp(db:3306)/banco_de_teste?parseTime=true&loc=UTC' \
  -v "$PWD":/src -w /src golang:1.26-alpine go test -p 1 ./...
# testes com contêineres reais de bancos (baixa imagens): adicione -e CORUJA_TEST_DOCKER=1 -v /var/run/docker.sock:/var/run/docker.sock
./scripts/gen-proto.sh                                        # regenera internal/pb (só se mudar o .proto)
```

---
Coruja Backup - Versão 0.0.13 - Desenvolvido por [Henrique Fagundes](https://henrique.tec.br) · Licença MIT
