# syntax=docker/dockerfile:1
# Coruja Backup · build único com dois alvos:  --target server  |  --target agent

# --- interface web (React) -> internal/webui/dist ----------------------------------------------------------------
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY web/ ./
RUN mkdir -p /src/internal/webui && npm run build

# --- binários Go -----------------------------------------------------------------------------------------------------
FROM golang:1.24-alpine AS gobuild
ARG VERSION=0.0.5
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags "-s -w -X github.com/AprendendoLinux/corujabackup/internal/version.Version=${VERSION}" -o /out/coruja-server ./cmd/server \
 && go build -trimpath -ldflags "-s -w -X github.com/AprendendoLinux/corujabackup/internal/version.Version=${VERSION}" -o /out/coruja-agent ./cmd/agent

# --- Server ------------------------------------------------------------------------------------------------------------
FROM alpine:3.21 AS server
ARG VERSION=0.0.5
LABEL org.opencontainers.image.title="Coruja Backup Server" \
      org.opencontainers.image.description="Backup centralizado: orquestração, agendamentos, nuvem e interface web" \
      org.opencontainers.image.source="https://github.com/AprendendoLinux/corujabackup" \
      org.opencontainers.image.version="${VERSION}" org.opencontainers.image.licenses="MIT"
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 coruja
COPY --from=gobuild /out/coruja-server /usr/local/bin/coruja-server
RUN mkdir -p /data && chown coruja /data
VOLUME /data
USER coruja
EXPOSE 8080 8443
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 CMD ["/usr/local/bin/coruja-server", "-healthcheck"]
ENTRYPOINT ["/usr/local/bin/coruja-server"]

# --- Agente ------------------------------------------------------------------------------------------------------------
FROM alpine:3.21 AS agent
ARG VERSION=0.0.5
LABEL org.opencontainers.image.title="Coruja Backup Agent" \
      org.opencontainers.image.description="Agente leve do Coruja Backup (conecta ao Server por IP:porta)" \
      org.opencontainers.image.source="https://github.com/AprendendoLinux/corujabackup" \
      org.opencontainers.image.version="${VERSION}" org.opencontainers.image.licenses="MIT"
# Clientes de banco para o modo "rede" (no modo contêiner o dump roda com as ferramentas do próprio contêiner).
RUN apk add --no-cache ca-certificates tzdata mariadb-client postgresql17-client mongodb-tools && mkdir -p /var/lib/coruja-agent
COPY --from=gobuild /out/coruja-agent /usr/local/bin/coruja-agent
VOLUME /var/lib/coruja-agent
# Roda como root: a Fase 2 precisa ler pastas do host (montadas somente leitura).
ENTRYPOINT ["/usr/local/bin/coruja-agent"]
