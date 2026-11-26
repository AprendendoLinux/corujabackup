#!/bin/sh
# Publicação manual das imagens multi-arquitetura (o fluxo normal é o workflow "Release", disparado pela tag vX.Y.Z).
# Uso: ./scripts/release.sh 0.0.17      (precisa de "docker login ghcr.io" e do buildx)
set -e
V="${1:?informe a versão, ex.: 0.0.17}"
OWNER="${OWNER:-aprendendolinux}"
cd "$(dirname "$0")/.."
# Segredo do app OAuth padrão do Google (opcional): GOOGLE_CLIENT_SECRET_FILE=/caminho/arquivo ./scripts/release.sh X.Y.Z
SEC=()
if [ -n "${GOOGLE_CLIENT_SECRET_FILE:-}" ]; then SEC+=(--secret "id=google_client_secret,src=$GOOGLE_CLIENT_SECRET_FILE"); fi
if [ -n "${GOOGLE_WEB_CLIENT_SECRET_FILE:-}" ]; then SEC+=(--secret "id=google_web_client_secret,src=$GOOGLE_WEB_CLIENT_SECRET_FILE"); fi
for t in server agent; do
  docker buildx build "${SEC[@]}" --platform linux/amd64,linux/arm64 --target "$t" --build-arg "VERSION=$V" \
    --provenance=mode=max --sbom=true \
    -t "ghcr.io/$OWNER/corujabackup-$t:$V" -t "ghcr.io/$OWNER/corujabackup-$t:latest" --push .
done
