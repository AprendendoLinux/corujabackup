# Segurança

## Modelo de ameaças (resumo)
| Ameaça | Proteção |
| :--- | :--- |
| Alguém cria o administrador antes de você | Código de configuração por execução (nos logs) + limite de tentativas |
| Adivinhar senhas | argon2id; limite de tentativas por IP e por usuário; mensagens iguais para "usuário" e "senha" errados |
| Roubo de senha | 2FA (TOTP) com códigos de recuperação e anti-reuso; política para exigir 2FA; sessões listáveis e revogáveis |
| Sequestro de sessão / CSRF / XSS | Cookie `HttpOnly` + `SameSite=Strict` (+ `Secure` em HTTPS); cabeçalho CSRF e checagem de `Origin`; CSP restritiva; logo SVG servida isolada (`sandbox`); saída HTML sempre escapada |
| Vazamento do banco MySQL | Tokens de nuvem, senhas SMTP/Telegram e de bancos **cifrados** (AES-256-GCM); a chave fica no volume `/data`, fora do banco |
| Vazamento de credenciais pela API/logs | Segredos nunca voltam pela API; erros passam por filtro que remove senha/token |
| Servidor falso se passando pelo Server | O agente **fixa a impressão digital** do certificado (vem dentro do token de matrícula): sem "confiar no primeiro uso" |
| Token de matrícula roubado | Uso único, expira (até 7 dias), só o hash é guardado; revogar um agente derruba o canal na hora |
| Server comprometido pedindo coisas ao agente | Listas de permissão no agente (`CORUJA_ALLOWED_PATHS`, `CORUJA_ALLOWED_CONTAINERS`); destino sempre confinado à pasta de backups; nomes de arquivo validados |
| Arquivo malicioso no backup (restauração) | Extração bloqueia `..`, caminhos absolutos e escrita através de links; nunca sobrescreve |
| Injeção de comando nos jobs de banco | Sem shell com dados do usuário: argumentos em lista, nomes validados, segredos por variável de ambiente |
| Dependências vulneráveis | `govulncheck` e `npm audit` no CI (semanal e a cada PR) |

## O que **não** está coberto (leia)
- **Quem controla o Server controla os agentes:** ele define o que será salvo e para onde. Por isso as **listas de permissão do agente** existem: use-as.
- **`docker.sock` = controle total do Docker do host.** Só monte o socket no agente se precisar de jobs de banco em contêineres e restrinja `CORUJA_ALLOWED_CONTAINERS`.
- O agente roda como **root** (precisa ler arquivos de outros donos). O compose reduz as capacidades a `DAC_READ_SEARCH` e `DAC_OVERRIDE`.
- As credenciais de banco ficam em texto no disco do **agente** (`/var/lib/coruja-agent/jobs.json`, permissão 600): quem controla o agente já alcança o banco.
- Os backups em `/backups` e na nuvem **não são cifrados por este sistema** (a segurança vale a da pasta e da conta). Se precisar de criptografia do arquivo, cifre a pasta/volume ou use a criptografia do provedor.
- Não há "login por SSO". O Server não faz limitação de taxa geral na API (só no login/matrícula): coloque-o atrás de um proxy se for exposto à internet.

## Checklist de endurecimento
- [ ] HTTPS na interface (proxy reverso com `CORUJA_TRUST_PROXY=true`, ou `CORUJA_TLS_CERT/KEY`).
- [ ] Porta `8080` acessível só a quem administra; `8443` só aos servidores com agente (firewall).
- [ ] 2FA ativo e **exigido** (Segurança > Exigir 2FA); sessão curta (ex.: 8 h).
- [ ] Backup do banco **e** do volume `/data` guardados **fora** do servidor (veja Operação).
- [ ] Agentes com `CORUJA_ALLOWED_PATHS` / `CORUJA_ALLOWED_CONTAINERS` e `/host` montado só com o necessário.
- [ ] Contas de nuvem com acesso mínimo (Google: escopo `drive.file`; OneDrive: `Files.ReadWrite`).
- [ ] Imagens fixadas por versão (nunca `latest` em produção) e atualizadas com o [CHANGELOG](../CHANGELOG.md) em mãos.
- [ ] Revisar a página Auditoria periodicamente; usuários em perfis mínimos (Operador/Somente leitura).

Para relatar uma vulnerabilidade, veja [SECURITY.md](../SECURITY.md).

## Exceção do Dropbox

No Google Drive e no OneDrive o agente recebe só o endereço de uma sessão de envio. O Dropbox não oferece isso para arquivos grandes, então o Server emite para o agente um **token de curta duração (cerca de 4 horas)** com o único escopo `files.content.write`, restrito à pasta do aplicativo. Esse escopo permite gravar e também **apagar** arquivos dessa pasta: um agente comprometido poderia remover backups da conta Dropbox durante a validade do token. Atenuantes: o Dropbox mantém os arquivos apagados recuperáveis por um período (conforme o plano da conta), e o token não lê o conteúdo nem alcança o restante do Dropbox. Se isso não for aceitável, use outro destino para esse job.

## Destinos de rede

Em SFTP, FTP, SMB e pasta local o **agente recebe as credenciais do destino** (exceção à regra de que credenciais ficam só no Server): sem isso ele não conseguiria gravar numa rede que o Server não alcança. Use uma conta exclusiva para backups, com acesso apenas à pasta de destino. Um agente comprometido pode ler, alterar e apagar o que essa conta alcança; se o armazenamento oferecer snapshots ou imutabilidade, habilite. Veja [DESTINOS.md](DESTINOS.md).

## Navegação de pastas pela interface
Para "clicar e selecionar", o Server pede ao agente a listagem de uma pasta.
- O que trafega: **nomes** de pastas e arquivos e o tamanho dos arquivos, nunca o conteúdo. Nada é guardado no Server.
- Quem pode: perfis **Operador** e **Administrador** (os mesmos que já podem criar um job com qualquer caminho permitido e baixar o resultado). Somente leitura não navega.
- No agente: a listagem fica presa à raiz do host (ou à pasta de backups), **não segue atalhos** e respeita `CORUJA_ALLOWED_PATHS` (só aparecem os itens permitidos e o caminho até eles).
- Pastas de destino: nas nuvens quem lista é o Server; nos destinos de rede, o agente do job, com as mesmas credenciais usadas no envio.
