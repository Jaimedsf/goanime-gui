<p align="center">
    <a href="https://github.com/Jaimedsf/goanime-gui/blob/main/LICENSE"><img src="https://img.shields.io/github/license/Jaimedsf/goanime-gui" alt="Licença"></a>
    <img src="https://img.shields.io/github/last-commit/Jaimedsf/goanime-gui" alt="Último commit">
    <img src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white" alt="Go 1.27">
</p>

# GoAnime

> **Fork não oficial** de [alvarorichard/GoAnime](https://github.com/alvarorichard/GoAnime),
> com um aplicativo desktop (Wails) por cima do mesmo núcleo. O motor de busca,
> as fontes e o player vêm do projeto original, que este repositório acompanha
> por merge; problemas do app de terminal ou das fontes provavelmente pertencem
> lá. Distribuído sob a mesma [licença MIT](LICENSE), com os créditos originais.

O GoAnime permite procurar animes, filmes e séries e reproduzir ou baixar
episódios direto no mpv. Ele coleta dados de várias fontes para oferecer
conteúdo legendado e dublado em inglês e português.

São dois aplicativos sobre a mesma base:

*   **`goanime`** — interface de texto (TUI) para o terminal.
*   **`goanime-gui`** — aplicativo desktop (Wails), com catálogo, calendário
    semanal de lançamentos, favoritos e histórico.

## Índice

1.  [Recursos](#recursos)
2.  [Pré-requisitos](#pré-requisitos)
3.  [Compilando](#compilando)
4.  [Como usar](#como-usar)
5.  [Uso avançado](#uso-avançado)
6.  [Aviso](#aviso)
7.  [Créditos](#créditos)
8.  [Contribuindo](#contribuindo)

## Recursos

*   Busca de animes, filmes e séries por nome
*   Pesquisa simultânea em todas as fontes ativas por padrão
*   Suporte a conteúdo legendado e dublado em inglês e português
*   Reprodução online com qualidade selecionável (1080p, 720p, etc.)
*   Download único ou em lote de múltiplos episódios
*   Integração com Discord RPC
*   Rastreamento de progresso (retomar reprodução e salvar histórico no SQLite)
*   Upscaling integrado (Anime4K) para melhorar a qualidade de vídeo
*   Cache local de capas, miniaturas e metadados, para o aplicativo abrir sem
    rebaixar tudo de novo — e funcionar sem rede

## Pré-requisitos

*   [mpv](https://mpv.io/) — reprodutor de mídia, versão atualizada
*   [Go](https://go.dev/dl/) 1.27 ou superior, para compilar

No Windows, o `mpv` precisa estar no `PATH` do sistema.

## Compilando

Este repositório não publica binários prontos, então a instalação é a partir
do código:

```bash
git clone https://github.com/Jaimedsf/goanime-gui.git
cd goanime-gui
```

### Aplicativo de terminal

```bash
go build -o goanime ./cmd/goanime
```

### Aplicativo desktop

O frontend são módulos ES escritos à mão, embutidos no binário — não há
bundler nem etapa de build de JavaScript:

```bash
go build -o goanime-gui ./cmd/goanime-gui
```

Os scripts em [`build/`](build/) cobrem os empacotamentos por sistema
operacional (`buildlinux.sh`, `buildmacos.sh`, `buildwindows.ps1`).

## Como usar

1.  **Abra o terminal.**
2.  **Inicie o aplicativo:** digite `goanime` e aperte `Enter`.
3.  **Pesquise:** escreva o nome do anime que deseja assistir.
4.  **Selecione:** navegue pela lista de resultados com as setas do teclado e
    aperte `Enter` para prosseguir.
5.  **Assista:** escolha o episódio, defina a qualidade e o vídeo será
    executado imediatamente no `mpv`.

No aplicativo desktop, basta abrir o `goanime-gui` e usar a busca ou o
catálogo.

## Uso avançado

### Busca direta

Para pesquisar direto da linha de comando, informe um título:

```bash
goanime "Naruto"
```

### Menu de ajuda

```bash
goanime -h
```

## Aviso

O GoAnime não hospeda, armazena nem distribui nenhum vídeo. Ele apenas
localiza links publicados por sites de terceiros e os abre no seu player; a
disponibilidade e a legalidade desse conteúdo são responsabilidade desses
sites e de quem os acessa, conforme a lei do seu país.

## Créditos

*   [alvarorichard/GoAnime](https://github.com/alvarorichard/GoAnime) e seus
    contribuidores — o projeto original, do qual vem todo o núcleo.
*   [Wails](https://wails.io/), [mpv](https://mpv.io/) e
    [Anime4K](https://github.com/bloc97/Anime4K).

## Contribuindo

Antes de iniciar qualquer trabalho, leia o
[Guia de desenvolvimento](docs/Development.md).

Início rápido:

1.  Crie sua branch a partir de `main`, no formato `tipo/descrição-curta`
    (`git checkout -b feat/minha-mudanca`).
2.  Padronize o código com `go fmt` e rode `npm run check` ao mexer no
    frontend.
3.  Faça commits no formato Conventional Commits.
4.  Abra um pull request apontando para `main`.
