# Destinos de rede e pasta local

Além das nuvens (Google Drive, OneDrive e Dropbox), o Coruja Backup grava em **SFTP**, **FTP/FTPS**, **SMB/CIFS** e numa **pasta local** do agente (que cobre **NFS** e **iSCSI** montados no host).

## Quem faz o quê

Um NAS ou servidor interno normalmente só é alcançado pela rede do agente. Por isso, nesses destinos **o agente** envia, confere e aplica a retenção. O Server guarda a configuração **cifrada** e a entrega ao agente pelo canal TLS (no agente ela fica em arquivo com permissão 600). Para o teste na tela **Destinos**, escolha o agente que vai acessar o destino.

Cada envio segue a ordem: grava como `nome.coruja-part` → confere → renomeia → **só então** aplica a retenção (que nunca apaga o arquivo mais novo e só mexe nos arquivos do próprio job).

| Conferência | O que faz |
| :--- | :--- |
| **Completa** (padrão) | Relê o arquivo do destino e compara o SHA-256. Dobra o tráfego. |
| **Rápida** | Compara só o tamanho. |

## SFTP
- Senha ou chave privada (sem frase-senha). Caminho relativo à pasta do usuário; comece com `/` para um caminho absoluto.
- No primeiro teste, a **impressão digital** do servidor é mostrada para você confirmar e fica fixada. Se o servidor mudar de chave (reinstalação ou ataque), o envio é recusado; crie o destino de novo depois de conferir.

## FTP / FTPS
- Padrão: **FTPS explícito**. Há também o implícito (porta 990) e o FTP sem criptografia, que envia **senha e arquivos em texto puro** (use apenas em rede isolada).
- "Aceitar certificado autoassinado" cifra o tráfego, mas não verifica a identidade do servidor.
- Usa modo passivo: o servidor precisa liberar as portas passivas para o agente.

## SMB / CIFS
- SMB 2/3 com NTLM, direto do agente (não precisa montar nada nem de privilégios). Informe servidor, compartilhamento, usuário, senha e, se houver, domínio.

## Pasta local (NFS, iSCSI, disco externo)
O agente **só grava dentro de `/destinos`** (mude com `CORUJA_DEST_DIR`), e não segue links simbólicos para fora dela. Monte o armazenamento **no host** e exponha só o que for destino:

```yaml
    volumes:
      - /mnt/nas:/destinos/nas
```

- **NFS:** monte no host (`mount -t nfs servidor:/export /mnt/nas`, ou no `/etc/fstab` com `_netdev`).
- **iSCSI:** conecte o alvo no host (`open-iscsi`), formate e monte o dispositivo.
- Montar dentro do contêiner exigiria privilégios de administrador, o que o projeto evita de propósito.

> Se o volume **não estiver montado** no host, o agente pode acabar gravando no disco do próprio host (na pasta vazia do ponto de montagem). Use uma subpasta que só exista dentro do volume montado como pasta base: se a montagem falhar, o teste acusa e você percebe.

## Restauração
O Server não acessa destinos de rede, então não há download direto pelo navegador. Na execução, use **Trazer do destino para o agente** (o arquivo é conferido pelo SHA-256) e depois **Extrair no agente**, ou copie o arquivo.
