# Arquitetura e decisões

| Tema | Decisão | Motivo |
| :--- | :--- | :--- |
| Linguagem (Server e Agente) | Go, um módulo só | binário estático (~15 MB), pouca RAM no agente, concorrência nativa, fácil imagem mínima |
| Comunicação | gRPC em stream bidirecional sobre TLS, **iniciado pelo agente** | funciona atrás de NAT/firewall; comandos e progresso tipados; um canal por agente |
| Porta | uma só no Server (UI, REST e gRPC) | exigência de configuração mínima |
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
