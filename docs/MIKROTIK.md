# Backup de roteadores MikroTik

O Coruja Backup salva roteadores MikroTik (RouterOS 6 e 7). Quem acessa o roteador é o **agente**, por **SSH**: escolha um agente que alcance o roteador pela rede. Cada job salva um roteador.

## O que é salvo

| Item | Arquivo no backup | Para que serve |
| :--- | :--- | :--- |
| Export da configuração | `config.rsc` | Texto legível, sem senhas. Bom para conferir mudanças e migrar para outro modelo. |
| Export com dados sensíveis | `config-com-dados-sensiveis.rsc` | Igual, com senhas, chaves e segredos (PPP, Wi-Fi, IPsec...). Restaura tudo, mas o arquivo contém segredos. |
| Backup binário | `<identidade>.backup` | Cópia completa do RouterOS, com usuários e certificados, **cifrada com a senha do job**. Só restaura em equipamento do mesmo modelo. |
| Identificação | `info.txt` | Identidade, versão, modelo, número de série, data e como restaurar. |

Tudo vai num `.tar.gz` comum, conferido (SHA-256 e conteúdo) e guardado como os outros backups: pasta local, storages (inclusive com gravação direta) e nuvens, com a mesma retenção. Roteadores são sempre salvos por inteiro (não há incremental: a configuração é pequena).

O `.backup` é gerado no roteador com um nome temporário, baixado por SFTP e **apagado do roteador** em seguida, mesmo se algo falhar.

## Preparar o roteador

Crie um usuário só para o backup, com as permissões necessárias:

```
/user group add name=coruja-backup policy=ssh,ftp,read,write,policy,test,sensitive
/user add name=coruja group=coruja-backup password=SENHA-FORTE
```

- `ssh`: acesso; `read`: export; `sensitive`: export com dados sensíveis; `ftp`: baixar o `.backup` (SFTP); `write`, `policy` e `test`: gerar o `.backup`.
- O serviço SSH precisa estar ativo (`/ip service`). Restrinja-o ao IP do agente, se possível (`/ip service set ssh address=IP-DO-AGENTE/32`).
- Também é possível entrar com chave SSH (importe a chave pública no usuário: `/user ssh-keys import`).

## Criar o job

Em **Jobs › Novo job**, escolha **Roteador MikroTik** e informe o endereço, a porta SSH, o usuário e a senha (ou a chave). Marque o que salvar e, para o `.backup`, defina uma senha de pelo menos 8 caracteres. **Guarde essa senha**: sem ela o `.backup` não restaura.

Clique em **Testar conexão com o roteador**: o agente conecta, lê a identificação e confere as permissões do export, sem gravar nada no roteador. Na primeira vez, confirme a **impressão digital** do roteador: ela fica fixada e, se mudar (roteador reinstalado ou ataque), o backup é recusado até você testar e salvar o job de novo.

As credenciais ficam cifradas no Server e viajam para o agente só pelo canal TLS (no agente, em arquivo com permissão 600).

## Restaurar

Em **Histórico**, abra a execução e use **Extrair no agente** (o que estiver só no storage de rede é trazido sozinho) ou baixe o arquivo. Depois, envie ao roteador (Winbox: Files, ou SFTP/SCP).

- **`.backup`** (mesmo modelo): `/system backup load name=<arquivo>.backup password=<senha do job>`. O roteador reinicia com a configuração do backup.
- **`.rsc`**: o export é a configuração **completa**, para aplicar num roteador **zerado**. Por cima de uma configuração existente, o `/import` para no primeiro item repetido. Zere e aplique de uma vez:

  ```
  /system reset-configuration no-defaults=yes skip-backup=yes run-after-reset=<arquivo>.rsc
  ```

  Se as interfaces ainda não existirem quando o script rodar no boot, acrescente `:delay 15s` na primeira linha do arquivo. Num roteador já sem configuração, basta `/import file-name=<arquivo>.rsc`.

Os dois caminhos foram testados num RouterOS 7.16 real: o `.backup` carregado com a senha devolveu a configuração alterada, e o `.rsc` importado num roteador de fábrica recriou identidade, endereços, segredos PPP e grupos de usuários.
