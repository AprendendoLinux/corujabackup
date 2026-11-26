#!/bin/sh
# Publicação manual das imagens multi-arquitetura (o fluxo normal é o workflow "Release", disparado pela tag vX.Y.Z).
# Uso: ./scripts/release.sh 0.0.8      (precisa de "docker login ghcr.io" e do buildx)
set -e
V="${1:?informe a versão, ex.: 0.0.8}"
OWNER="${OWNER:-aprendendolinux}"
cd "$(dirname "$0")/.."
for t in server agent; do
  docker buildx build --platform linux/amd64,linux/arm64 --target "$t" --build-arg "VERSION=$V" \
    --provenance=mode=max --sbom=true \
    -t "ghcr.io/$OWNER/corujabackup-$t:$V" -t "ghcr.io/$OWNER/corujabackup-$t:latest" --push .
done
