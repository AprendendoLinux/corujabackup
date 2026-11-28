# Temas (white-label)

Um **pacote de tema** é um arquivo `.zip` que muda, de uma vez, a aparência da **interface web**, dos **e-mails** e dos **relatórios em PDF**:
cores, logo e metadados. Ele é importado em **Aparência › Importar pacote (.zip)** (somente administradores).

**Temas embutidos** (escolha direto em **Aparência**, sem pacote), cada um com modo claro e escuro:

| Tema | Estilo |
| :--- | :--- |
| **Coruja** | Roxo e âmbar (padrão). |
| **Floresta** | Verde e dourado. |
| **Oceano** | Azul-petróleo e coral. |
| **Grafite** | Cinza-chumbo e azul: sóbrio, bom para ambiente corporativo. |
| **Bordô** | Vinho e dourado. |
| **Terracota** | Argila e lilás. |
| **Ônix** | Preto profundo, monocromático, com detalhes dourados. **Sempre escuro** (o modo claro não se aplica). |

Oceano, Grafite, Bordô, Terracota e Ônix têm o contraste conferido pelas regras WCAG AA nos dois modos (texto, texto secundário, botões e mensagens de erro).

Pacotes prontos estão na pasta [`themes/`](../themes): `exemplo` (neutro, ponto de partida), `melloait`, `umami` e `clube-naval`.

## Estrutura do pacote

O `.zip` é **plano** (sem pastas) e contém:

| Arquivo | Obrigatório | Para quê |
| :--- | :---: | :--- |
| `theme.json` | sim | Manifesto: identificação, cores e referência aos arquivos. |
| logo da interface (ex.: `logo.png`, `logo.svg`) | não | PNG, JPEG, GIF, WebP ou SVG (sem scripts). Cabeçalho, tela de login e ícone da aba do navegador. |
| logo de e-mail/PDF (ex.: `logo.png`) | não | Só **PNG, JPEG ou GIF** (clientes de e-mail e o PDF não renderizam SVG/WebP). Se faltar e a logo da interface for PNG/JPEG/GIF, ela é reaproveitada. |
| `preview.png` | não | Miniatura (apenas informativa). |
| `README.md`, `LICENSE` | não | Ignorados pelo sistema. |

Limites: `.zip` até 3 MiB; cada arquivo até 1 MiB; até 12 arquivos; nomes só com letras, números, `.`, `_` e `-`.

## `theme.json`

```json
{
  "schema": 1,
  "id": "minha-marca",
  "name": "Minha Marca",
  "version": "1.0.0",
  "author": "Seu nome",
  "description": "Texto curto (até 300 caracteres).",
  "colors": {
    "bg": "#f4f6f8", "surface": "#ffffff", "text": "#1b2430", "muted": "#66727f", "border": "#dde3e9",
    "primary": "#2a6f97", "primary_contrast": "#ffffff", "accent": "#e9a23b", "ok": "#2f9e62", "danger": "#cf4b4b"
  },
  "document": { "primary": "#2a6f97", "accent": "#e9a23b" },
  "logo": { "ui": "logo.png", "document": "logo.png" },
  "preview": "preview.png"
}
```

| Campo | Regras |
| :--- | :--- |
| `schema` | Sempre `1`. |
| `id` | 2 a 30 caracteres: minúsculas, números e hífen. Identifica o tema; importar outro pacote com o mesmo `id` **atualiza** o existente. Não pode ser o de um tema embutido: `coruja`, `noturno`, `floresta`, `oceano`, `grafite`, `bordo`, `terracota` ou `onix` (nem esses com `-escuro`). |
| `name` | Nome exibido (1 a 40 caracteres). |
| `version`, `author`, `description` | Opcionais, informativos. |
| `colors` | As 10 cores da interface, **todas obrigatórias**, no formato `#RRGGBB`. |
| `colors_dark` | Opcional. As mesmas 10 cores para o **modo escuro**. Sem ele, o modo escuro é gerado automaticamente a partir de `colors` (fundos escuros tingidos pela cor principal e destaques clareados para manter o contraste). |
| `document` | Opcional. Cores de e-mail e PDF que você quer **sobrescrever** (veja abaixo). |
| `logo.ui`, `logo.document` | Opcionais. Nomes de arquivos que estão no pacote. |

Campos desconhecidos são **recusados** (evita erro de digitação silencioso). O JSON Schema está em [`theme.schema.json`](theme.schema.json).

### Tokens de cor

| Token | Onde aparece |
| :--- | :--- |
| `bg` | Fundo da página. |
| `surface` | Cartões, janelas e campos. |
| `text` | Texto principal. |
| `muted` | Texto secundário e legendas. |
| `border` | Bordas e divisórias. |
| `primary` | Botões, links e item de menu ativo. |
| `primary_contrast` | Texto sobre a cor principal (precisa ter bom contraste com `primary`). |
| `accent` | Destaques. |
| `ok` / `danger` | Sucesso e erro/alerta. |

### E-mail e PDF

E-mails e PDFs usam **sempre fundo claro** (legível em qualquer cliente de e-mail e na impressão). Por isso:

- Em um tema **claro** (`bg` com luminosidade alta), o relatório usa as mesmas cores de `colors`.
- Em um tema **escuro**, o relatório aproveita `primary`, `accent`, `ok` e `danger` e usa o fundo claro padrão.
- A chave opcional `document` ajusta cada cor do relatório (`bg`, `text`, `muted`, `border`, `primary`, `accent`, `ok`, `danger`). Use-a, por exemplo, para trocar um `primary` amarelo (ruim com texto branco) por um tom mais escuro nos documentos.
- A logo de documento é usada no cabeçalho do e-mail e do PDF.

O tema que vale para e-mail e PDF é o **tema padrão** (em Aparência › Identidade); a interface respeita ainda a escolha de cada pessoa no seletor do topo.

> Fontes personalizadas ainda **não** são suportadas (a versão 1 do formato só cobre cores e logos).

## Modo claro e escuro
Todo tema tem os dois modos. Em **Aparência**, o administrador escolhe o tema e o modo: **Claro**, **Escuro** ou **Automático**, que segue o modo do sistema operacional de cada pessoa. Os temas só entram por pacote; na tela não é possível alterar as cores de um tema instalado, apenas instalar, atualizar (importando de novo o mesmo `id`) ou excluir. E-mails e PDF usam sempre o modo claro.

## Passo a passo: criar e importar um tema

1. Copie a pasta [`themes/exemplo`](../themes/exemplo) e renomeie.
2. Edite `theme.json` (`id`, `name`, cores) e troque `logo.png` pela sua logo (fundo transparente, altura em torno de 80 a 200 px).
3. Valide e gere o pacote (não precisa de banco de dados):
   ```bash
   docker run --rm -v "$PWD":/t ghcr.io/aprendendolinux/corujabackup-server:latest \
     theme pack /t/minha-marca /t/minha-marca.zip
   ```
   (ou, para só validar um `.zip` existente: `... theme validate /t/minha-marca.zip`). Também dá para criar o `.zip` com qualquer ferramenta, desde que os arquivos fiquem na **raiz** do arquivo.
4. Na interface: **Aparência › Importar pacote (.zip)**. A página já passa a usar o tema para você conferir.
5. Para aplicar ao e-mail e ao PDF: no tema, **Ver cores › Usar como padrão** e **Salvar aparência**.
6. Para atualizar uma marca: ajuste a pasta, gere o `.zip` de novo e importe outra vez (mesmo `id`).

## Segurança da importação

O pacote é tratado como dado não confiável: tamanho e quantidade limitados, nomes de arquivo restritos (sem `..`, sem pastas, sem links simbólicos), formato das imagens verificado pelo **conteúdo**, SVG com script, referências externas ou entidades é recusado e **não existe CSS livre** (só cores validadas). Logos são servidas com `Content-Security-Policy: sandbox` e `nosniff`. A importação e a remoção ficam na auditoria.

## Marcas de terceiros

Os pacotes `melloait`, `umami` e `clube-naval` incluem logotipos e cores de empresas, como exemplos de identidade visual. A licença MIT do projeto **não** se aplica a esses logotipos, que continuam sendo marcas de seus titulares; remova o pacote se não tiver o direito de usá-lo.
