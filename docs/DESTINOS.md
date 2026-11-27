# Storages de rede e pasta local

Além das nuvens (Google Drive, OneDrive e Dropbox), o Coruja Backup grava em **SFTP**, **FTP/FTPS**, **SMB/CIFS**, **NFS** e numa **pasta local** da máquina do agente (outro disco, um volume iSCSI ou um HD USB já montados). Tudo é configurado na tela **Storages**: nada precisa ser montado na stack do agente.

## Quem faz o quê

Um NAS ou servidor interno normalmente só é alcançado pela rede do agente. Por isso, nesses storages **o agente** grava, confere e aplica a retenção. O Server guarda a configuração **cifrada** e a entrega ao agente pelo canal TLS (no agente ela fica em arquivo com permissão 600). Para testar um storage, escolha o agente que vai acessá-lo.

Cada envio segue a ordem: grava como `nome.coruja-part` → confere → renomeia → **só então** aplica a retenção (que nunca apaga o arquivo mais novo e só mexe nos arquivos do próprio job). Se algo falhar no meio (inclusive um cancelamento), o temporário é apagado e os backups antigos ficam intactos.

| Conferência | O que faz |
| :--- | :--- |
| **Completa** (padrão) | Relê o arquivo do storage e compara o SHA-256. Dobra o tráfego. |
| **Rápida** | Compara só o tamanho. |

## Como gravar: gerar no agente ou direto no storage

Nos storages desta página (não nas nuvens), o job escolhe **como gravar**:

- **Gerar no agente e depois enviar** (padrão): o `.tar.gz` é gerado e conferido na pasta do backup local, depois enviado. Permite manter cópias no agente, para restaurar mais rápido.
- **Gravar direto no storage:** o `.tar.gz` vai em fluxo para o storage, **sem arquivo no agente**. Útil quando a máquina do agente tem pouco espaço livre. Na conferência completa, o arquivo é relido do storage e conferido pelo SHA-256 **e** pela estrutura do tar (todos os itens legíveis) antes de receber o nome final; a retenção só roda depois disso.
  - Jobs de banco ainda fazem o dump numa pasta temporária do agente (apagada ao final); só o pacote final vai direto.
  - Se a rede cair no meio, o backup daquela execução falha (não há cópia local para reenviar depois). A próxima execução agendada tenta de novo.
  - Exige agente **0.0.19** ou mais novo.

## SFTP
- Senha ou chave privada (sem frase-senha). Caminho relativo à pasta do usuário; comece com `/` para um caminho absoluto.
- No primeiro teste, a **impressão digital** do servidor é mostrada para você confirmar e fica fixada. Se o servidor mudar de chave (reinstalação ou ataque), o envio é recusado; crie o storage de novo depois de conferir.

## FTP / FTPS
- Padrão: **FTPS explícito**. Há também o implícito (porta 990) e o FTP sem criptografia, que envia **senha e arquivos em texto puro** (use apenas em rede isolada).
- "Aceitar certificado autoassinado" cifra o tráfego, mas não verifica a identidade do servidor.
- Usa modo passivo: o servidor precisa liberar as portas passivas para o agente.

## SMB / CIFS
- SMB 2/3 com NTLM, direto do agente (não precisa montar nada nem de privilégios). Informe servidor, compartilhamento, usuário, senha e, se houver, domínio.

## NFS
O agente fala **NFSv3 direto pela rede**, com um cliente embutido: não monta nada na máquina, não precisa de pacotes no host nem de privilégios extras no contêiner. Informe o servidor e a **exportação** (como aparece em `showmount -e`).

No servidor NFS, libere o IP do agente em `/etc/exports` e recarregue com `exportfs -ra`:

```
/srv/backups  10.0.0.5(rw,sync,no_subtree_check)
```

- **Quem grava (UID/GID):** o NFS identifica quem grava por números. O padrão é 0 (root), mas com `root_squash` (o padrão dos servidores) o root vira "nobody". Informe em "Usuário que grava (avançado)" o UID/GID do dono da pasta exportada, ou use `all_squash,anonuid=…,anongid=…` na exportação.
- **Portas:** o agente consulta o portmapper (porta 111) para achar o MOUNT e o NFS (normalmente 2049). Libere essas portas do agente até o servidor. Se o servidor não tiver portmapper, informe a porta do NFS.
- **Porta de origem:** a maioria das exportações só aceita clientes vindos de portas abaixo de 1024. O agente usa essas portas automaticamente (o Docker as libera dentro dos contêineres, mesmo com `cap_drop: ALL`). Se o agente rodar fora do Docker sem root, use a opção `insecure` na exportação.
- Só **NFSv3** com `sec=sys` (AUTH_SYS), que é o padrão de Linux, Synology, QNAP, TrueNAS e Windows Server. Não funcionam: servidores só NFSv4 (inclusive **AWS EFS** e **Azure Files NFS**) e exportações com Kerberos (`sec=krb5`, `krb5i`, `krb5p`).
- **Segurança:** no NFSv3 com `sec=sys`, o servidor confia no UID informado pelo cliente e os dados trafegam **sem criptografia**. Libere a exportação só para o IP do agente e use uma rede interna confiável (de preferência uma VLAN de backup).

## Pasta local
Qualquer pasta da máquina do agente: outro disco, um volume **iSCSI** ou um **HD USB** já montados no host. Escolha o agente e a pasta no navegador de pastas, que mostra as partições e o espaço livre. Cada job grava numa subpasta dela.

- Usa a mesma montagem gravável da pasta do backup local (`- /:/host-rw`, padrão da stack) e respeita `CORUJA_BACKUP_ROOTS`, quando definido.
- O storage guarda o caminho; os jobs que o usarem gravam nesse mesmo caminho na máquina do **seu** agente.
- **iSCSI:** conecte o alvo no host (`open-iscsi`), formate e monte o dispositivo; depois escolha a pasta.
- Os backups gravados nessa pasta ficam fora das origens automaticamente (não entram em um backup do `/`).

> Se o disco **não estiver montado** no host, a pasta escolhida passa a ficar no disco do sistema (na pasta vazia do ponto de montagem). Escolha uma subpasta que só exista dentro do disco montado: se a montagem falhar, o teste e o backup acusam.

Storages "pasta local" antigos (criados antes da 0.0.19, dentro de `/destinos`) continuam funcionando.

## Restauração
O Server não acessa storages de rede, então não há download direto pelo navegador. Na execução, use **Trazer do storage para o agente** (o arquivo é conferido pelo SHA-256) e depois **Extrair no agente**, ou copie o arquivo. Isso vale também para backups gravados direto no storage.
