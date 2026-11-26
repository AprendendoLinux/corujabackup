# Perguntas frequentes

**Preciso abrir portas nos servidores com agente?** Não. O agente só faz conexões de saída para o Server (`8443`).

**O arquivo de backup passa pelo Server?** Não. O agente envia direto à nuvem por uma sessão emitida pelo Server; as credenciais da nuvem ficam só no Server.

**E se o Server cair no horário do backup?** O agente guarda a agenda e roda sozinho; o resultado (com o log completo) fica numa fila local e é entregue quando o Server voltar.

**Como sei que o backup realmente restaura?** Em jobs de banco ative o **teste de restauração real**: o backup é restaurado num servidor descartável (sem rede) e o número de tabelas é comparado com a origem. Para arquivos, use **Extrair no agente** no detalhe da execução.

**Posso usar outra nuvem?** Hoje: OneDrive e Google Drive. A arquitetura é modular (`cloud.Provider`); novos provedores entram sem mexer no núcleo.

**Por que o Google pede "TVs e dispositivos de entrada limitada"?** Porque a autorização é por código de dispositivo (sem URL de retorno), ideal para sistemas hospedados em qualquer endereço. O escopo é só `drive.file`.

**Perdi o `secret.key`.** Os dados cifrados (nuvens, senhas) ficam ilegíveis: reconecte as nuvens e recadastre senhas de canais/bancos. Faça backup do volume `/data`.

**Como mudo o fuso dos horários?** Cada job e cada regra de relatório têm o seu fuso (padrão `America/Sao_Paulo`).

**Dá para usar com Docker Swarm/Kubernetes?** O Server é um contêiner comum com 1 volume e 1 MySQL. O agente precisa de acesso ao sistema de arquivos do nó (DaemonSet). Não há manifestos prontos ainda.

**Quanto espaço o histórico ocupa?** Pouco: ~1 KB por linha de log; execuções são mantidas por 400 dias.

**Como atualizo?** Veja [Operação](OPERACAO.md#atualização). Sempre faça backup do banco antes.
