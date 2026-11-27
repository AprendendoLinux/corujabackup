# Changelog

## [0.0.27] - Restaurar numa pasta escolhida
- **Restaurar numa pasta escolhida do servidor:** na restauração dos itens marcados (e em "Restaurar esta versão"), a nova opção **Numa pasta do servidor** abre o navegador de pastas do agente, com o espaço livre do disco e a criação de pasta nova. Os itens entram na pasta a partir de onde estavam (ex.: `/home/ana/.ssh` → `/root/restaurado/.ssh`); arquivos com o mesmo nome lá são substituídos e o resto da pasta não é tocado. Só administradores, com confirmação e registro na auditoria; precisa do volume `/:/host-rw` e respeita `CORUJA_BACKUP_ROOTS`. **Exige agente 0.0.27.**

## [0.0.26] - Desmarcar subpastas e padrões de exclusão
- **Desmarcar subpastas na árvore:** dentro de uma pasta marcada, desmarque uma subpasta ou um arquivo para deixá-lo de fora (aparece riscado, com "fica de fora"). Dá para marcar de novo algo dentro de uma subpasta desmarcada. O total estimado já desconta o que ficou de fora, e o que foi desmarcado aparece na lista do job como "Fica de fora", com ✕ para voltar a incluir.
- **Exclusões só por padrão:** a seção virou "Padrões de exclusão", com apenas o botão "Adicionar um padrão": `*.log` (arquivos por nome), `cache/` (pastas com esse nome, em qualquer lugar) e `*/www/tmp/` (pastas por caminho).
- **Padrões de pasta (`cache/`, `tmp/`) no agente:** antes, um padrão terminado em `/` nunca batia. **Exige agente 0.0.26** (o Server recusa salvar esses padrões para agentes mais antigos, para não ignorá-los sem avisar).

## [0.0.25] - Instalação guiada, cancelamento imediato e tamanho das pastas
- **Cancelar backup agora vale na hora:** antes, o agente só via o pedido de cancelamento entre um arquivo e outro, então no meio de um arquivo grande (dezenas de GB) o cancelamento demorava minutos e a tela não dava nenhum sinal. Agora a leitura para em menos de um segundo, o log mostra "cancelamento pedido" e, até o agente confirmar que parou, a execução aparece como **"Cancelando…"** para todos (Histórico, Jobs e detalhe, com a faixa "Cancelando, aguarde…"), mesmo fechando e abrindo a janela. O arquivo incompleto é apagado. Vale também para restaurações e downloads. **Exige agente 0.0.25.**
- **Tamanho das pastas no seletor do que salvar:** cada pasta mostra o tamanho, calculado pelo agente em segundo plano (a árvore abre na hora, com "calculando…" em cada pasta). O rodapé mostra o total estimado do que está marcado. O cálculo não atravessa para outras partições, ignora `/proc`, `/sys` e `/dev`, conta hard links uma vez só, roda com prioridade de disco ociosa e para em 20 s por pasta (aí mostra "mais de X"). O resultado fica guardado por 5 minutos. **Exige agente 0.0.25** (com agente antigo, os tamanhos simplesmente não aparecem).
- **Instalação do agente mais simples:** o assistente de novo agente agora pergunta primeiro como você quer instalar (**docker-compose.yml** para Portainer/docker compose ou **docker run**) e mostra só o comando escolhido, já com o token. O acesso ao Docker (para bancos em contêineres) virou uma caixa de marcar, em vez de uma linha comentada, e o comando `docker run` saiu quebrado em linhas, sem rolagem lateral.

## [0.0.24] - Restauração de bancos e downloads
- **Restaurar bancos de dados pela interface** (MySQL, MariaDB, PostgreSQL e MongoDB): na página Restaurar, escolha o backup, marque os bancos e restaure no mesmo servidor do job. O padrão é **num banco novo** (`<banco>_restaurado_<data>`), sem tocar no original; administradores também podem **sobrescrever o original** (o banco é apagado e recriado como no backup), com confirmação e registro na auditoria. No fim, a quantidade de tabelas/coleções é comparada com a do backup. No Histórico, **Restaurar banco…** leva direto à página.
- **Baixar pelo navegador:** na navegação de um backup, **Baixar arquivo** (um arquivo, como está) ou **Baixar .zip** (arquivos e pastas marcados), com cada item na versão daquele backup, inclusive nos incrementais. O conteúdo vem do agente em fluxo, num canal próprio que não atrapalha o resto, e nada fica gravado no Server. Endereço de uso único, registro na auditoria.
- **Restaurar esta versão** e **Baixar** direto na lista de versões de um arquivo.
- **Tamanho de cada pasta** (MB/GB e quantidade de arquivos) na navegação.
- Backups de banco anteriores a esta versão também funcionam: a lista de bancos é lida do próprio arquivo.
- **Exige agente 0.0.24** para downloads e restauração de bancos.

## [0.0.23] - Restauração por seleção
- **Nova página Restaurar:** escolha o job e um ponto na **linha do tempo** (cada backup, agrupado por dia, full ou incremental) e **navegue nos arquivos como estavam naquele momento**, pasta por pasta, com tamanho e data. Nos incrementais, o que mudou naquele backup aparece marcado.
- **Versões de um arquivo:** veja todas as versões de um arquivo nos backups do job (data, tamanho e em quantos backups aparece) e abra o backup de qualquer uma delas.
- **Restaure só o que marcar** (arquivos e pastas): numa **pasta de restauração** no agente (seguro, nada no servidor muda) ou, só para administradores, **no local original**, sobrescrevendo (com confirmação e registro na auditoria). Cada arquivo sai do pacote certo da cadeia, sem aplicar a semana inteira, e o que estiver só no storage de rede é trazido sozinho. O andamento aparece na própria janela.
- **Backups antigos:** os anteriores a esta versão não têm índice; a página oferece **Gerar índice**, que o agente monta lendo o próprio arquivo.
- No Histórico, **Navegar e restaurar arquivos** leva direto à página.
- A extração (inclusive "Extrair no agente") grava cada arquivo num temporário e o renomeia no fim, e devolve o dono original quando o agente roda como root.
- **Exige agente 0.0.23.** Depois de atualizar, o primeiro backup de cada job incremental é full (o formato interno do retrato mudou).

## [0.0.22] - Backup de MikroTik
- **Roteadores MikroTik (RouterOS 6 e 7):** novo tipo de job **Roteador MikroTik**. O agente acessa o roteador por SSH e salva o **export da configuração** (`.rsc`), o **export com dados sensíveis** (senhas e chaves) e o **backup binário** (`.backup`, cifrado com a senha do job), mais um `info.txt` com a identificação e como restaurar. Tudo num `.tar.gz` conferido, com storages, gravação direta e retenção como os outros backups.
- O `.backup` é gerado no roteador com nome temporário, baixado por SFTP e **apagado do roteador** em seguida, mesmo em caso de falha.
- **Teste pelo painel** (sem gravar nada no roteador) e **impressão digital fixada**: se a chave do roteador mudar, o backup é recusado. Mensagens claras para senha errada, SSH desligado e falta de permissão (com as políticas necessárias).
- **Restauração testada num RouterOS real:** `.backup` carregado com a senha e `.rsc` importado num roteador de fábrica. Guia em `docs/MIKROTIK.md` e no Histórico.
- **Extrair no agente** agora também traz sozinho do storage de rede um backup **completo** (antes, só os incrementais), conferido pelo SHA-256 e apagado depois. Resolve a restauração de jobs com gravação direta.
- **Exige agente 0.0.22** para jobs de MikroTik.

## [0.0.21] - Full + incrementais
- **Backups incrementais:** no job, escolha **Full + incrementais** e o dia da semana do full (padrão: domingo). Nos outros dias, só entram os arquivos novos ou alterados (pela data, tamanho e inode) e a lista do que foi apagado. Menos espaço e envios bem mais rápidos.
- **Full automático quando preciso:** no dia marcado, no primeiro backup, quando as origens ou exclusões mudam e quando o último full tem 7 dias ou mais (o do dia marcado não rodou). Na lista de jobs, **Executar backup full agora** abre uma nova semana na hora.
- **Seguro contra falhas:** o agente só considera um backup como base dos próximos depois que ele termina bem (arquivo e storage conferidos). Arquivos que não puderam ser lidos voltam no incremental seguinte.
- **Retenção por semana:** num job incremental, local e no storage, a retenção conta semanas (o full e os incrementais dela) e apaga semanas inteiras, nunca deixando um incremental sem o seu full. Jobs só de fulls continuam como antes.
- **Restauração de um incremental:** "Extrair no agente" aplica em ordem o full e os incrementais até o escolhido e remove o que foi apagado na origem. O que estiver só no storage de rede (inclusive com gravação direta) é trazido sozinho, conferido e apagado depois. O guia manual também explica a ordem.
- No Histórico, cada execução mostra se foi **full** ou **incremental**.
- O incremental é um `.tar.gz` comum (`...-inc.tar.gz`), com um `.coruja-manifest.json` na raiz.
- **Exige agente 0.0.21** para jobs incrementais (o painel avisa e não deixa salvar com um agente antigo). Jobs de banco de dados continuam sempre completos.

## [0.0.20] - Escolha do período
- **Período à escolha no Painel e no Histórico:** atalhos (Hoje, Ontem, Últimos 7, 14 ou 30 dias, Este mês, Mês passado) e um **calendário** para marcar o dia inicial e o final com cliques (até 366 dias). Os dias são contados no fuso de quem vê.
- **Painel:** indicadores, gráficos e saúde dos jobs seguem o período escolhido; as comparações usam o período anterior de mesmo tamanho, com as datas indicadas na tela. A escolha fica lembrada neste navegador (atalhos são recalculados a cada dia).
- **Histórico:** o período soma aos filtros de job e de estado; "Limpar filtros" volta a mostrar tudo. Com período, até 500 execuções por consulta (aviso quando houver mais).

## [0.0.19] - NFS, pasta local livre e gravação direta
- **NFS pela interface:** novo storage **NFS** (NFSv3). O agente fala o protocolo direto pela rede, com um cliente embutido: não monta nada na máquina, não precisa de pacotes no host nem de privilégios extras. Informe servidor, exportação e, se a exportação usar `root_squash`, o UID/GID de quem grava. O teste mostra o espaço livre da exportação. Mensagens claras para exportação não liberada, sem permissão, somente leitura, portmapper inacessível e Kerberos (não suportado).
- **Pasta local livre:** o storage "pasta local" agora é **qualquer pasta da máquina do agente** (outro disco, iSCSI ou HD USB já montados), escolhida no navegador de pastas com as partições e o espaço livre. Não precisa mais de volume em `/destinos` na stack (os storages antigos continuam funcionando). Os backups gravados nela ficam fora das origens automaticamente.
- **Gravar direto no storage:** nos storages de rede e pasta local, o job pode **gravar o backup direto no storage, sem arquivo no agente** (ideal quando falta espaço nele). O arquivo é gravado como `.coruja-part`, relido do storage e conferido (SHA-256 e todos os itens do tar) antes de receber o nome final; a retenção só roda depois disso, e uma falha ou cancelamento não deixa nada pela metade. "Trazer do storage" funciona igual. Jobs de banco fazem o dump numa pasta temporária e enviam só o pacote final.
- **Painel com gráficos:** indicadores do período (taxa de sucesso, execuções, falhas e perdidos, volume gerado), cada um comparado com o período anterior; gráficos de **execuções por dia por estado** e de **volume gerado por dia**, com dica ao passar o mouse (ou focar pelo teclado) e a tabela com os mesmos números; **saúde de cada job** (última execução, taxa de sucesso, tamanho, duração e storage) e **uso dos storages**. Períodos de 7, 14 ou 30 dias, agrupados no fuso de quem vê. Cores dos estados validadas para daltonismo, com modo claro e escuro.
- **PDF redesenhado:** cabeçalho com a marca, cartão de situação com ícone e resumo em frase, indicadores, quadro "Precisa de atenção", gráfico das execuções dos últimos 14 dias, estados das últimas 24 h, maior volume por job, tabelas listradas (com a próxima execução de cada job), medidores de uso dos storages e rodapé com paginação "Página X de Y".
- **Nome e sobrenome:** em Minha conta, cada usuário informa nome e sobrenome, que aparecem no menu lateral, no cabeçalho da conta e em Usuários (o administrador também pode editar). O login continua o mesmo. O cadastro de usuário e a configuração inicial pedem os dois.
- **Correção:** um envio a SFTP, FTP, SMB ou pasta local **cancelado no meio** deixava o arquivo temporário `.coruja-part` no storage; agora ele é apagado.
- Os logs dos storages de rede dizem "storage" em vez de "nuvem".
- **Exige agente 0.0.19** para NFS, pasta local escolhida e gravação direta (o painel avisa e não deixa salvar com um agente antigo).

## [0.0.18] - Agenda por cliques e partições
- **Agenda por cliques:** Todo dia, Dias da semana, Todo mês ou Várias vezes ao dia, com dias e intervalo escolhidos em botões e o horário digitado (hh:mm, 24 h). A expressão cron não aparece mais; agendas antigas que a tela não consegue representar são avisadas e trocadas só ao salvar.
- **Partições na escolha da pasta local:** o seletor mostra cada partição montada na máquina do agente (/, /boot, /mnt/...) com a barra de uso (o mesmo percentual do `df`), o espaço livre e o tipo; clicar abre a partição. O espaço mostrado junto da pasta diz de qual partição ele é. **Exige agente 0.0.18.**
- **Retenção mais clara:** "Manter as últimas N cópias no agente" ou "apagar depois de enviar"; no storage, "as últimas N cópias", "tudo dos últimos N dias" ou os dois juntos, com um resumo em frase.
- **Destinos agora se chamam Storages.**

## [0.0.17] - O Server conecta nos agentes
- **O Server conecta nos agentes:** o agente é cadastrado na interface com **nome, IP e porta** (assistente Novo agente), e a stack dele fica só com a porta publicada e o token. O Server disca cada agente, tenta de novo sozinho com espera crescente e mostra na tela por que não conectou (tempo esgotado, conexão recusada, autenticação recusada).
- **Segurança:** o agente só aceita a conexão de quem prova ser o seu Server (TLS 1.3 com o certificado fixado no token); o Server autentica o agente pelo token e depois pela credencial, como antes. A porta do agente deve ficar liberada só para o IP do Server.
- **Porta padrão do agente: `23847`**, sem uso por nenhum serviço registrado na IANA e fora das faixas de portas temporárias do Linux e do Windows.
- O Server não expõe mais porta para os agentes (o `8443` sai do compose do Server); a opção "Endereço para os agentes" saiu, porque não há mais comandos que apontem para o Server.
- **Atualização:** agentes já matriculados não precisam de token novo. Atualize a imagem, troque `CORUJA_SERVER` por `ports: ["23847:23847"]`, mantenha o volume e informe o IP e a porta do agente na interface (veja docs/OPERACAO.md).

## [0.0.16] - Interface nova
- **Interface nova:** menu lateral à esquerda, com ícones e grupos (Operação e Administração), que pode ser recolhido; no celular vira uma gaveta. Cartões, login e painel redesenhados.
- **Ícones no lugar dos links** de ação (executar, histórico, editar, pausar, excluir, testar...), com dica ao passar o mouse ou focar pelo teclado.
- **Tema único do sistema:** o seletor de tema do topo saiu. O tema é escolhido só em **Aparência**, pelo administrador, e vale para todos. **Correção:** a escolha antiga guardada em cada navegador podia sobrepor o tema do sistema, mostrando um tema diferente do selecionado; ela é descartada.
- **Créditos do rodapé configuráveis** em Aparência (texto, nome e link), também nos e-mails e no PDF. Nome vazio esconde os créditos.
- **Janelas de confirmação no visual do sistema** no lugar dos alertas do navegador (excluir, executar, revogar, renomear, redefinir senha...).
- **Execução ao vivo:** em Jobs e no Histórico, o backup em andamento aparece com indicador girando, fase e percentual, atualizados a cada 2 segundos.
- **Temas só por pacote, com modo claro e escuro:** a tela de Aparência não cria nem edita cores; instala, atualiza ou exclui pacotes, além dos temas da Coruja (Coruja e Floresta). Cada tema tem modo claro e escuro (`colors_dark` no pacote, ou gerado automaticamente), e o modo pode ser Claro, Escuro ou Automático. O antigo tema "Noturno" vira Coruja no modo escuro.
- **Assistentes:** Novo job (Identificação › Origem › Agenda › Destino › Revisão) e Novo agente (Identificação › Instalação › Conexão, que espera o agente aparecer) passam a ser em etapas. Na edição de um job, qualquer etapa pode ser aberta e salva.
- **Foto do usuário:** em Minha conta, cada usuário envia a própria foto (PNG, JPEG, GIF ou WebP, até 2 MB; o sistema recorta o centro e reduz para 256×256 px), mostrada no menu e em Usuários.

## [0.0.15] - Agente e tipos na lista
- **Pasta e arquivo diferenciados:** nas listas do que salvar e das exclusões, cada item tem ícone e etiqueta (Pasta, Arquivo, Atalho, Padrão), e as pastas terminam com `/`. Um caminho que não existe mais no agente aparece como **Não encontrado**.
- **Nomes:** a máquina onde o backup roda é sempre chamada de **agente** na interface, nos e-mails e no PDF. "Servidor" fica só para servidores SMTP, SFTP, FTP, SMB e de banco.

## [0.0.14] - Pasta local pelo painel
- **Segurança:** a pasta de dados do agente (credencial e jobs, com senhas de bancos e destinos) nunca entra num backup, mesmo selecionando o `/` inteiro.
- **Pasta do backup local escolhida no painel:** navegue pelos discos do servidor, crie a pasta e veja o **espaço livre** antes de escolher. Exige o novo volume `- /:/host-rw` na stack do agente (dá permissão de gravação no servidor; `CORUJA_BACKUP_ROOTS` limita onde). A origem continua somente leitura. Jobs antigos continuam na pasta padrão.
- **Servidor inteiro:** a stack padrão mostra o `/` completo; sistemas de arquivos virtuais (`/proc`, `/sys`, `/dev`...) e as visões montadas dos contêineres ficam fora do backup automaticamente.
- **Correção:** salvar uma pasta que contém a pasta de backups do agente (ex.: `/srv` com os backups em `/srv/coruja-backups`) incluía os backups anteriores e o que estava sendo gerado. Agora o agente deixa de fora, automaticamente, a pasta de backups, a raiz de destinos locais (`/destinos`) e tudo o que estiver montado dentro delas, e registra isso no log da execução. Reconhece a pasta pela identidade no disco, qualquer que seja o caminho usado na stack. Uma origem escolhida **dentro** dessas pastas é ignorada com aviso. **Atualize os agentes.**

## [0.0.13] - Clicar e selecionar
- **Clicar e selecionar:** no job, as pastas e arquivos a salvar e as exclusões são marcados numa árvore do servidor do agente; a **pasta de destino no agente** e a **pasta no destino** (nuvens, SFTP, FTP, SMB e pasta local) são escolhidas num navegador de pastas, que também cria pasta nova. Digitar continua possível para padrões (`*.tmp`) e casos especiais.
- A navegação mostra só nomes e tamanhos, respeita `CORUJA_ALLOWED_PATHS`, não segue atalhos e exige perfil Operador ou Administrador. **Atualize os agentes** para usá-la.
- **Endereço para os agentes:** o administrador pode informar o endereço:porta usado nos comandos de instalação (para quando o canal é publicado em outra porta ou outro nome).
- **Correção:** os botões "Copiar" não faziam nada quando a interface era aberta por `http://IP:porta` (o navegador só libera a área de transferência moderna em HTTPS ou `localhost`). Agora funcionam nos dois casos e mostram "Copiado ✓".

## [0.0.12] - Destinos de rede
- **Destinos de rede:** SFTP (chave do servidor fixada), FTP/FTPS, SMB/CIFS e pasta local (NFS/iSCSI via volume montado em `/destinos`). O agente grava com nome temporário, confere relendo o arquivo (SHA-256) ou por tamanho, renomeia e só então aplica a retenção; "Trazer do destino" para restaurar. Testados contra servidores reais (`scripts/test-netdest.sh`).
- A página **Nuvem** passou a se chamar **Destinos**.
- Janelas com título fixo e barra de rolagem discreta; a escolha de destino cabe sem rolar.
- **Atualize os agentes** para usar os destinos de rede.

## [0.0.11] - Temas: Clube Naval e ícone da aba
- **Ícone da aba acompanha a logo** do tema ativo (ou a logo enviada em Aparência); sem logo própria, continua a coruja.
- **Tema Clube Naval** em `themes/clube-naval` (botões no vermelho da âncora do brasão e dourado; escudo na interface e logo completa em e-mail e PDF).

## [0.0.10] - Fase 9 · Login com um clique e Dropbox
- **Dropbox:** novo destino, com login por popup (cliente público, sem segredo) e acesso só à pasta do aplicativo. Envio em partes com retomada, conferência pelo `content_hash` do Dropbox, retenção, download e restauração. Como o Dropbox não tem endereço pré-autorizado para arquivos grandes, o agente recebe um **token de curta duração só de escrita de conteúdo** (exceção à regra "o agente não recebe tokens"); cada envio precisa terminar antes de o token vencer (cerca de 4 horas).
- **Correção (todas as nuvens):** quando o agente detectava que o arquivo na nuvem não conferia, ele desistia sem avisar o Server e o arquivo corrompido ficava com nome de backup. Agora o Server confere de novo e o apaga, preservando as cópias boas.
- **Login por popup** para Google Drive, OneDrive e Dropbox (e "Reautorizar" usa o popup nessas contas); tela de escolha de destino com ícones.
- **Aplicativo OAuth padrão do Google Drive** nas imagens oficiais: o usuário só abre o link e digita o código, sem registrar aplicativo (o modo manual continua disponível).

## [0.0.9] - Fase 8 · Pacotes de temas
- **Pacotes de tema (`.zip`):** importados em Aparência; o tema vale para a **interface**, os **e-mails** e os **PDFs** (cores, logo de interface e logo de documento). Importar o mesmo `id` atualiza o tema.
- **Validação rígida:** `.zip` plano e limitado, nomes e tipos verificados, imagens identificadas pelo conteúdo, SVG com script/referência externa recusado, sem CSS livre; auditoria de importação.
- **Cores próprias de e-mail/PDF** (`document`) e logo própria de documento por tema; metadados de pacote não podem ser forjados pelo cliente.
- **CLI:** `coruja-server theme validate|pack` (sem banco de dados).
- **Pacotes incluídos** em `themes/`: `exemplo`, `melloait` e `umami`. Documentação completa em `docs/TEMAS.md` e JSON Schema em `docs/theme.schema.json`.
- **Build das imagens sem emulação:** os estágios de build rodam na plataforma nativa e só o binário Go é cruzado (o workflow de release travava no arm64 sob QEMU).
- **Roadmap revisado** (fases 9 a 12): destinos de armazenamento, full/incremental + MikroTik, restauração inteligente e piloto.

## [0.0.8] - Fase 7 · Endurecimento e publicação
- **Dependências:** gRPC atualizado de 1.72 para 1.85 e projeto migrado para **Go 1.26** — o `govulncheck` apontava 8 vulnerabilidades
  alcançáveis no gRPC (incluindo bypass de autorização e falhas no servidor HTTP/2); agora **zero**. CI roda `govulncheck` e `npm audit`.
- **Configuração inicial protegida:** o administrador só é criado com um **código mostrado nos logs** do Server (muda a cada início),
  com limite de tentativas — ninguém "rouba" o primeiro acesso.
- **Segurança de acesso:** página **Segurança** (política de **exigir 2FA** para ninguém/administradores/todos, duração da sessão),
  **sessões ativas** em Minha conta (listar, encerrar uma ou todas as outras), IP do cliente correto atrás de proxy (vale o último
  `X-Forwarded-For`, não o forjado), cabeçalhos extras (`Permissions-Policy`, COOP/CORP, HSTS em HTTPS, CSP mais restrita),
  timeouts e limites no servidor HTTP, tetos de transmissões ao vivo, **HTTPS direto** (`CORUJA_TLS_CERT/KEY`) e **TLS com o MySQL** (`CORUJA_DB_TLS`).
- **Agente com política local:** `CORUJA_ALLOWED_PATHS` e `CORUJA_ALLOWED_CONTAINERS` limitam o que o agente aceita fazer, mesmo se o Server pedir outra coisa.
- **Operação:** `coruja-server admin reset-password|create-admin` (recuperação de acesso), **métricas Prometheus** em `/metrics` com token,
  exportação da **auditoria em CSV** (com neutralização de fórmulas), limpeza periódica (auditoria 400 dias, limitadores de tentativas),
  aviso no início se a chave de criptografia não abre os dados guardados e **recusa de iniciar com banco migrado por versão mais nova**.
- **Imagens e compose endurecidos:** Server `read_only`, `cap_drop: ALL`, `no-new-privileges`, sem root; agente com `cap_drop: ALL` +
  `DAC_READ_SEARCH`/`DAC_OVERRIDE`. Publicação **multi-arquitetura** (amd64/arm64) automatizada por GitHub Actions, com SBOM e proveniência.
- **Documentação:** README reescrito, Instalação, Operação, Segurança (modelo de ameaças e checklist), FAQ, SECURITY.md e CONTRIBUTING.md.

## [0.0.7] - Fase 6 · Notificações e relatórios
- **Canais configurados pela interface:** **e-mail (SMTP)** com STARTTLS, SSL/TLS direto ou sem criptografia (a senha nunca vai sem TLS
  a menos que você escolha "sem criptografia"; certificado autoassinado é opção explícita) e **Telegram** (bot + vários chats, com
  reenvio como texto simples se o HTML for recusado). Credenciais cifradas no banco e nunca devolvidas pela API; erros não vazam
  senha nem token. Botão **Enviar teste** manda um relatório de exemplo com PDF.
- **Regras:** falha/backup perdido, com avisos, sucesso (por job ou todos), **relatório diário consolidado** (horário e fuso
  próprios, PDF anexo opcional, "só se houver problemas") e **agente offline** (um alerta por queda). Regras recomendadas com um clique.
- **Relatório A4 em PDF** gerado pelo próprio Server (sem Chromium): primeira página = resumo executivo (estado geral, contadores,
  atenção, jobs, nuvem com cota, agentes); demais páginas = execuções do período. Usa nome, **logo** e cores escolhidos em Aparência.
  E-mail em HTML (tabelas + CSS inline, logo embutida) com versão em texto; mensagem de Telegram dentro do limite de 4096 caracteres.
- **Fila com novas tentativas:** cada envio é registrado (histórico na interface), com até 5 tentativas (1, 5, 15 e 30 min) e sem
  duplicar (chave única por regra/canal/evento). Entrega parcial no Telegram não repete para quem já recebeu.
- Nomes de jobs, nuvens, canais e regras aceitam acentos ("Relatório diário"); o nome do arquivo de backup é gerado sem acentos.

## [0.0.6] - Fase 5 · Interface completa
- **Logs ao vivo:** o agente envia as linhas do log a cada 2 s durante o backup e o Histórico as mostra em tempo real (SSE,
  com selo "ao vivo", rolagem automática e barra de progresso); cai para atualização comum se a transmissão falhar.
- **Identidade visual (white-label):** página **Aparência** (administradores) para trocar o **nome** do sistema, enviar uma
  **logo personalizada** (PNG, JPEG, GIF, WebP ou SVG sem scripts, até 1 MiB; o tipo é decidido pelo conteúdo e servida
  isolada, com CSP `sandbox`), escolher o **tema padrão** e criar até 10 **temas personalizados** com seletor de cores e
  pré-visualização imediata. Nome e logo aparecem no topo, no login e na aba do navegador; o rodapé padrão é mantido.
- **Restauração:** no detalhe de cada execução, **baixar da nuvem** (o Server só repassa o fluxo, sem gravar em disco; o token
  da nuvem nunca é enviado a endereços de redirecionamento), **reenviar para a nuvem** quando o envio falhou (reaproveita o
  arquivo local preservado e corrige o estado da execução), **extrair no agente** para `restores/<pasta>` (extração segura:
  bloqueia `..`, caminhos absolutos e escrita através de links; nunca sobrescreve) e **guia de restauração manual** com os
  comandos certos para arquivos, MySQL/MariaDB, PostgreSQL e MongoDB. Todas as ações ficam na auditoria e no log da execução.

## [0.0.5] - Fase 4 · Bancos de dados
- **Jobs de banco de dados** para **MySQL, MariaDB, PostgreSQL e MongoDB**, configurados na interface (escolha do tipo ao criar o job).
- **Dois modos:** *contêiner Docker* (o dump roda **dentro** do contêiner, com as ferramentas da versão certa, pela Docker Engine API;
  sem usuário/senha informados, usa as credenciais do próprio contêiner) ou *rede* (host e porta, com os clientes incluídos no agente).
  A interface busca os contêineres do agente e tem "Testar conexão" (lista os bancos e a versão).
- **Um arquivo por execução:** `.tar.gz` com um dump por banco + `manifest.json` (versão, tamanhos, SHA-256, nº de tabelas),
  seguindo o mesmo fluxo de verificação, envio à nuvem (conferido por hash), retenção local e na nuvem dos jobs de arquivos.
- **Conferência real de cada dump:** MySQL/MariaDB pelo marcador de conclusão (não confia só no código de saída), PostgreSQL com
  `pg_restore --list`, MongoDB pela assinatura e integridade do arquivo; PostgreSQL também salva papéis/globais.
- **Teste de restauração automatizado (opcional, recomendado):** restaura o backup num **servidor descartável** (contêiner sem rede,
  apagado depois), compara o número de tabelas/coleções com a origem e registra o resultado; falha vira aviso destacado.
- Segredos: a senha do banco fica cifrada no Server, nunca volta pela API, e vai ao agente só pelo canal TLS; os comandos recebem
  credenciais por variáveis de ambiente, não por argumentos.
- Testado com contêineres **reais** (MySQL 8.0, MariaDB 11.4, PostgreSQL 17, MongoDB 7.0), inclusive o fluxo completo até a nuvem.

## [0.0.4] - Fase 3 · Conectores de nuvem
- **OneDrive (Microsoft Graph) e Google Drive (Drive v3)**, arquitetura modular (`Provider`): conta, cota, sessão de upload,
  consulta de hashes, listagem e exclusão **definitiva** (sem lixeira, que continuaria ocupando a cota).
- **Autorização por código de dispositivo** (sem URL de retorno), na própria interface; tokens renovados automaticamente
  e **cifrados no banco** (AES-256-GCM, chave no volume `/data`). Google com escopo mínimo `drive.file`.
- **O agente envia direto para a nuvem** por uma sessão retomável emitida pelo Server: o agente nunca recebe tokens.
  Blocos de 10 MiB (OneDrive) / 8 MiB (Drive), retomada após falhas de rede e renovação de sessão expirada.
- **Conferência obrigatória:** o agente compara os hashes que a nuvem devolve (quickXorHash, SHA-1 ou MD5) e o Server
  confere de novo por conta própria; se não bater, o arquivo ruim é removido da nuvem e os backups antigos são preservados.
- **Retenção na nuvem** (por quantidade e/ou dias) só depois do envio verificado; o arquivo mais novo nunca é apagado.
  Cópia local configurável (0 = apagar após a nuvem confirmar; se o envio falhar, o arquivo local é preservado).
- Recusa o envio se a cota da conta não comporta o arquivo. Fases e progresso do envio aparecem no Histórico.
- Execuções órfãs (agente reiniciado no meio do backup) são marcadas como falha ao reconectar.
- Interface: página **Nuvem** (conectar, testar, reautorizar, cota) e seção de nuvem no formulário do job.

## [0.0.3] - Fase 2 · Agente e backups de arquivos
- **Jobs de arquivos** cadastrados na interface: pastas/arquivos, exclusões (`*.tmp`, `*/data/cache/*`), agenda cron com
  fuso horário, pasta de destino e quantidade de cópias a manter.
- **O agente agenda sozinho:** o Server envia as definições e o agente dispara no horário, mesmo com o Server fora do ar.
  Se ficou parado, recupera o último horário perdido (janela de 6 h). Um backup por vez; o mesmo job nunca roda em paralelo.
- **Execução:** `.tar.gz` com nome datado, escrito em arquivo temporário e renomeado só no final; **verificação** (SHA-256 +
  gzip + contagem de itens) antes de concluir; **retenção** só depois de verificado e só sobre arquivos do próprio job.
  Pasta de destino confinada à área de backups do agente. Cancelamento não deixa arquivo parcial.
- **Fila local de resultados:** o resultado (com o log completo) fica no disco do agente até o Server confirmar o recebimento
  (idempotente por UUID). Horários que o agente não cumpriu aparecem como **Perdido** no histórico.
- **Interface:** páginas **Jobs** (criar/editar/pausar/executar agora/excluir) e **Histórico** (filtros, detalhe com log,
  cancelar), estatísticas das últimas 24 h no Painel.

## [0.0.2] - Fase 1 · Núcleo do Server
- **Login e perfis:** configuração inicial (primeiro administrador), login com senha (argon2id), sessões por cookie
  `HttpOnly`/`SameSite=Strict`, perfis Administrador, Operador e Somente leitura, gestão de usuários, troca de senha.
- **2FA (TOTP, RFC 6238):** QR code, anti-replay, 8 códigos de recuperação; reset pelo administrador.
- **Proteções:** limite de tentativas de login (por IP e por usuário), erro genérico (sem enumeração de usuários),
  proteção CSRF (cabeçalho + Origin), cabeçalhos de segurança/CSP, trava do último administrador, auditoria.
- **Agentes:** matrícula por **token de uso único** que carrega a impressão digital do certificado do Server (o agente só
  precisa de IP:porta + token e confirma que fala com o Server certo, sem confiar no primeiro uso); credencial própria
  por agente (guardada só como hash), revogação imediata, renomear e excluir.
- **Canal gRPC/TLS 1.3** iniciado pelo agente (porta 8443): Hello, batimentos, estado online/offline, reconexão com espera
  crescente. Certificado autoassinado gerado na primeira execução (volume `/data`).
- **Interface:** telas de configuração, login (com 2FA), Painel, Agentes (gera o comando de instalação), Usuários,
  Auditoria e Minha conta.

## [0.0.1] - Fase 0 · Fundação
- Estrutura do projeto: Server e Agente em Go, interface web em React + TypeScript + Tailwind (embutida no binário do Server).
- Server: API `/api/health`, `/api/version`, `/api/branding`; conexão MySQL com espera e migrações embutidas.
- Interface: tema padrão **Coruja** (logo e paleta), temas Noturno e Floresta, rodapé padrão.
- Agente: verifica a conectividade com o Server (matrícula e jobs chegam nas próximas fases).
- Docker: imagens `server` e `agent` (build multi-stage), compose de produção com `mysql:latest` e rede `echelon`.
