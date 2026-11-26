# Contribuindo

1. Abra uma *issue* descrevendo a mudança (ou um *security advisory* para vulnerabilidades: veja [SECURITY.md](SECURITY.md)).
2. Crie uma branch, faça a mudança **com testes** e abra um *pull request* para `main`.
3. O CI roda `go vet`, `go test` (com MySQL) e o build da interface. Os testes de integração com bancos reais usam `CORUJA_TEST_DOCKER=1`.
4. Atualize o `CHANGELOG.md` e a documentação afetada. Textos de interface e documentação em **português do Brasil**.

Como rodar o ambiente de desenvolvimento e os testes: veja a seção *Desenvolvimento* do [README](README.md).
Decisões de arquitetura: [docs/ARQUITETURA.md](docs/ARQUITETURA.md).
