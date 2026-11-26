#!/bin/sh
# Gera internal/pb/*.pb.go a partir de proto/ (roda dentro de um contêiner Go; não exige nada instalado no host).
set -e
cd "$(dirname "$0")/.."
docker run --rm -v "$PWD":/src -w /src golang:1.26-alpine sh -c '
  apk add --no-cache protobuf protobuf-dev >/dev/null &&
  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.6 &&
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1 &&
  protoc -I proto --go_out=internal/pb --go_opt=paths=source_relative --go-grpc_out=internal/pb --go-grpc_opt=paths=source_relative proto/coruja/v1/agent.proto &&
  mv internal/pb/coruja/v1/*.go internal/pb/ && rm -rf internal/pb/coruja &&
  chown -R '"$(id -u):$(id -g)"' internal/pb'
