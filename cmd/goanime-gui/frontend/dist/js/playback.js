import { app, errText } from "./bridge.js";
import { PLAYERS } from "./labels.js";
import { STORAGE, readStorage, state, writeStorage } from "./state.js";
import { clear, els, episodeKey, setStatus, toast } from "./dom.js";
import { sourceInfo } from "./sources.js";

export function playerLabel(id) {
  const known = PLAYERS.find((p) => p.id === id);
  if (known) return known.label;
  // A custom path picked from the file dialog: show just the file name.
  return id.split(/[\\/]/).pop() || "mpv";
}

export function refreshPlayerChip() {
  els.playerName.textContent = playerLabel(state.player);
}

export function setPlayer(id) {
  state.player = id;
  writeStorage(STORAGE.player, id);
  refreshPlayerChip();
}

/** @type {((value: string | null) => void) | null} */
let playerModalResolve = null;

// openPlayerModal resolves to the chosen player id, or null if cancelled.
export async function openPlayerModal() {
  clear(els.choices);

  for (const p of PLAYERS) {
    const btn = document.createElement("button");
    btn.type = "button";
    if (p.id === state.player) btn.classList.add("current");

    const name = document.createElement("span");
    name.textContent = p.label;
    btn.appendChild(name);

    const tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = p.id === state.player ? "em uso" : p.note || "";
    if (p.id === state.player) tag.classList.add("active");
    btn.appendChild(tag);

    // Probe availability in the background and mark what is missing rather
    // than letting the user pick a player that cannot start.
    Promise.resolve()
      .then(() => app().PlayerAvailable(p.id))
      .then((ok) => {
        if (!ok) {
          tag.textContent = "não instalado";
          tag.className = "tag missing";
        }
      })
      .catch(() => {});

    btn.addEventListener("click", () => closePlayerModal(p.id));
    els.choices.appendChild(btn);
  }

  const browse = document.createElement("button");
  browse.type = "button";
  browse.textContent = "Escolher um programa…";
  browse.addEventListener("click", async () => {
    try {
      const path = await app().PickPlayer();
      if (path) closePlayerModal(path);
    } catch (err) {
      console.error(err);
      toast(`Não foi possível abrir o seletor de arquivos: ${errText(err)}`);
      closePlayerModal(null);
    }
  });
  els.choices.appendChild(browse);

  els.playerModal.hidden = false;
  // There is always at least the "procurar" button appended just above.
  /** @type {HTMLButtonElement} */ (
    els.choices.querySelector("button")
  ).focus();

  return new Promise((resolve) => {
    playerModalResolve = resolve;
  });
}

export function closePlayerModal(value) {
  els.playerModal.hidden = true;
  const resolve = playerModalResolve;
  playerModalResolve = null;
  if (resolve) resolve(value ?? null);
}

export async function playEpisode(li, ep) {
  // No open title means no source to play from; onEpisodeChosen keeps the
  // same guard before it gets here.
  if (!state.result) return;

  // Ask for a player once, then remember. The Player chip in the header is
  // how the choice gets changed later.
  if (readStorage(STORAGE.player, null) === null) {
    const chosen = await openPlayerModal();
    if (chosen === null) return;
    setPlayer(chosen);
  }

  const info = sourceInfo(state.result.source);
  const gated = info && info.browserGated;

  li.classList.add("busy");
  setStatus(
    gated
      ? `Preparando o episódio ${ep.number || ep.num} — uma janela de navegador pode abrir para passar pela verificação…`
      : `Abrindo o ${playerLabel(state.player)} no episódio ${ep.number || ep.num}…`,
    { busy: true }
  );

  try {
    await app().PlayEpisode(state.result, ep, state.player, state.quality);
    setStatus(`Reproduzindo o episódio ${ep.number || ep.num}.`);

    // The backend records the watch; reflect it without a full reload.
    state.watched.add(episodeKey(ep));
    li.classList.add("watched");
    if (!li.querySelector(".watched-tag")) {
      const tag = document.createElement("span");
      tag.className = "watched-tag";
      tag.textContent = "assistido";
      li.appendChild(tag);
    }
  } catch (err) {
    console.error(err);
    setStatus(`Falha na reprodução: ${errText(err)}`, { error: true });
  } finally {
    li.classList.remove("busy");
  }
}

export async function copyStreamURL(li, ep) {
  if (!state.result) return;
  li.classList.add("busy");
  setStatus(`Obtendo o link do episódio ${ep.number || ep.num}…`, {
    busy: true,
  });
  try {
    const url = await app().ResolveStreamURL(state.result, ep);
    await app().CopyToClipboard(url);
    toast("Link copiado para a área de transferência");
    setStatus("");
  } catch (err) {
    console.error(err);
    setStatus(`Não foi possível obter o link: ${errText(err)}`, {
      error: true,
    });
  } finally {
    li.classList.remove("busy");
  }
}
