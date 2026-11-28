#!/bin/sh
# Sobe servidores reais de SFTP, FTP/FTPS e SMB em contêiner, roda os testes de internal/netdest e internal/agentcore
# (backup em pasta nos storages) e remove tudo.
set -e
cd "$(dirname "$0")/.."
NET=coruja-nettest
cleanup() { docker rm -f ct-sftp ct-ftp ct-smb >/dev/null 2>&1 || true; docker network rm $NET >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup
docker network create $NET >/dev/null
docker run -d --name ct-sftp --network $NET atmoz/sftp:alpine 'coruja:s3nh@-forte:1001::upload' >/dev/null
docker run -d --name ct-ftp --network $NET -e PUBLICHOST=ct-ftp -e FTP_USER_NAME=coruja -e FTP_USER_PASS='s3nh@-forte' -e FTP_USER_HOME=/home/ftpusers/coruja \
  -e ADDED_FLAGS='--tls=1' -e TLS_CN=ct-ftp -e TLS_ORG=Coruja -e TLS_C=BR stilliard/pure-ftpd >/dev/null
docker run -d --name ct-smb --network $NET dperson/samba -u 'coruja;s3nh@-forte' -s 'backups;/share;yes;no;no;coruja' -p >/dev/null
docker exec ct-smb sh -c 'mkdir -p /share && chmod 777 /share'
# o Pure-FTPd gera parâmetros DH na primeira subida (pode levar alguns minutos): espera as portas abrirem
docker run --rm --network $NET alpine sh -c 'for h in ct-sftp:22 ct-ftp:21 ct-smb:445; do i=0; until nc -z -w 2 ${h%:*} ${h#*:}; do i=$((i+1)); [ $i -gt 200 ] && echo "sem resposta: $h" && exit 1; sleep 2; done; done'
sleep 2
docker run --rm --network $NET -e CORUJA_TEST_SFTP=ct-sftp -e CORUJA_TEST_FTP=ct-ftp -e CORUJA_TEST_SMB=ct-smb \
  -v "$PWD":/src -v coruja-gomod126:/go/pkg/mod -w /src golang:1.26-alpine go test -count=1 ./internal/netdest ./internal/agentcore "$@"
