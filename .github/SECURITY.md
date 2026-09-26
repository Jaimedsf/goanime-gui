# Política de segurança

## Versões suportadas

Correções de segurança saem na próxima release e entram na `main`. Antes de
reportar, confira se o problema acontece na
[release mais recente](https://github.com/Jaimedsf/goanime-gui/releases/latest) ou no último
commit da `main`.

| Versão                        | Suportada |
| ----------------------------- | --------- |
| Release mais recente          | Sim       |
| `main` (último commit)        | Sim       |
| Releases e commits anteriores | Não       |

## Como reportar uma vulnerabilidade

**Não abra uma issue pública** para problemas de segurança.

Use o reporte privado do GitHub:
[**Report a vulnerability**](https://github.com/Jaimedsf/goanime-gui/security/advisories/new)
(aba *Security* → *Advisories* → *Report a vulnerability*). O relato fica
visível só para você e para o mantenedor até ser publicado.

Inclua, se puder:

- o que o problema permite fazer e em que condições;
- os passos para reproduzir, ou uma prova de conceito;
- o commit em que você testou e o sistema operacional;
- se você já sabe, qual arquivo ou função está envolvido.

Pode escrever em português ou em inglês.

## O que esperar

Este é um projeto mantido por uma pessoa, no tempo livre. Os prazos abaixo são
o objetivo, não uma garantia:

- **Confirmação de recebimento:** em até 7 dias.
- **Avaliação inicial** (se é uma vulnerabilidade e qual a gravidade): em até
  14 dias.
- **Correção:** depende da gravidade e da complexidade. Você recebe notícias
  pelo próprio advisory enquanto a correção anda.

Quando a correção entrar na `main`, o advisory é publicado com o crédito para
quem reportou, a menos que você prefira ficar anônimo.

## Escopo

Este repositório é um [fork](https://github.com/alvarorichard/GoAnime) com um
aplicativo desktop por cima do mesmo núcleo.

**Reporte aqui** problemas no aplicativo desktop (`cmd/goanime-gui`,
`internal/guiapi`) ou em qualquer código que só exista neste fork.

**Reporte no projeto original**
([alvarorichard/GoAnime](https://github.com/alvarorichard/GoAnime/security))
problemas no núcleo compartilhado — app de terminal, fontes, player, download,
atualizador. A correção de lá chega aqui pelo sync com o upstream. Se não tiver
certeza de onde o problema está, reporte aqui que eu encaminho.

**Fora do escopo:**

- vulnerabilidades no `mpv`, no WebView2 ou em outros programas externos —
  reporte direto aos respectivos projetos;
- o conteúdo ou o comportamento dos sites de onde o app busca os vídeos;
- dependências com CVE conhecido que já têm um pull request aberto do
  Dependabot neste repositório.
