import { app, errText } from "./bridge.js";
import { FORMAT_LABELS, STATUS_LABELS } from "./labels.js";
import { STORAGE, state, writeStorage } from "./state.js";
import {
  clear,
  els,
  episodeKey,
  formatAired,
  plural,
  renderError,
  setStatus,
  skeletons,
} from "./dom.js";
import { artworkURL } from "./cards.js";
import { showEpisodes } from "./views.js";
import { sourceInfo } from "./sources.js";
import { isFavoriteResult, paintStar } from "./cards.js";
import { copyStreamURL, playEpisode } from "./playback.js";
import { downloadEpisode } from "./downloads.js";

export async function openResult(result) {
  state.result = result;
  showEpisodes(result.name);
  els.episodeFilter.hidden = true;
  els.episodeFilter.value = "";
  els.seasonWrap.hidden = true;
  els.episodesCount.textContent = "";

  els.titleMeta.hidden = true;
  refreshGateNote(result);
  loadTitleMeta(result);

  // Favorite state for the header star comes from the backend, which knows
  // the real key even for results without a URL.
  try {
    paintStar(els.favToggle, await app().IsFavorite(result));
  } catch {
    paintStar(els.favToggle, isFavoriteResult(result));
  }

  await refreshWatched(result);
  await loadEpisodes(result, "");
}

// loadTitleMeta fills the line under the episode heading with the release
// date and whatever else AniList knows. It runs detached: the episode list
// must never wait on a metadata lookup.
async function loadTitleMeta(result) {
  let info;
  try {
    info = await app().GetTitleInfo(result);
  } catch (err) {
    console.warn("GetTitleInfo failed", err);
    return;
  }
  // A different title may have been opened while this was in flight.
  if (!info || state.result !== result) return;

  const parts = [];
  if (info.releaseLabel) parts.push(`Lançado em ${info.releaseLabel}`);
  if (info.format) parts.push(FORMAT_LABELS[info.format] || info.format);
  if (info.status) parts.push(STATUS_LABELS[info.status] || info.status);
  if (info.episodeCount) parts.push(plural(info.episodeCount, "episódio"));
  if (info.score) parts.push(`${info.score}% no AniList`);

  if (!parts.length) return;

  clear(els.titleMeta);
  parts.forEach((p, i) => {
    if (i > 0) {
      const dot = document.createElement("span");
      dot.className = "dot";
      dot.textContent = "·";
      els.titleMeta.appendChild(dot);
    }
    els.titleMeta.appendChild(document.createTextNode(p));
  });
  els.titleMeta.hidden = false;
}

// refreshGateNote shows the live state of a source's human check instead of
// a fixed warning, so the user knows whether it will work here before
// pressing play.
export async function refreshGateNote(result) {
  const info = sourceInfo(result.source);
  if (!(info && info.browserGated)) {
    els.gateNote.hidden = true;
    return;
  }

  els.gateNote.hidden = false;
  els.gateNote.className = "note";
  els.gateMessage.textContent = "Verificando…";

  try {
    const st = await app().GetGateStatus(result.source);
    if (state.result !== result) return;
    paintGate(st);
  } catch (err) {
    els.gateMessage.textContent = errText(err);
  }
}

export function paintGate(st) {
  els.gateNote.className = `note ${st.ready ? "ready" : "blocked"}`;
  els.gateMessage.textContent = st.message || "";
}

async function refreshWatched(result) {
  try {
    state.watched = new Set((await app().WatchedEpisodes(result)) || []);
  } catch (err) {
    console.warn("WatchedEpisodes() failed", err);
    state.watched = new Set();
  }
}

export async function loadEpisodes(result, season) {
  skeletons(els.episodes, 8, "ep");

  // A browser-gated source may fall back to driving a real browser, which
  // can take minutes. Saying so beats a spinner that looks stuck.
  const info = sourceInfo(result.source);
  setStatus(
    info && info.browserGated
      ? `Carregando episódios de “${result.name}” — se a lista não vier rápido, uma janela de navegador pode abrir para a verificação; isso pode levar alguns minutos…`
      : `Carregando episódios de “${result.name}”…`,
    { busy: true }
  );

  // Artwork and the episode list are independent; start both and join
  // before rendering so the grid never paints twice.
  const artPromise = Promise.resolve()
    .then(() => app().GetEpisodeArt(result))
    .catch(() => ({ poster: result.imageURL || "", thumbs: {} }));

  try {
    const list = season
      ? await app().GetSeasonEpisodes(result, season)
      : await app().GetEpisodes(result);

    state.episodeArt = (await artPromise) || { poster: "", thumbs: {} };
    state.episodes = list.episodes || [];
    state.seasons = list.seasons || [];
    state.season = list.season || "";

    renderSeasons();
    renderEpisodes(state.episodes);

    els.episodesCount.textContent = state.episodes.length
      ? plural(state.episodes.length, "episódio")
      : "";
    els.episodeFilter.hidden = state.episodes.length < 12;
    setStatus(state.episodes.length ? "" : "Nenhum episódio encontrado.");
  } catch (err) {
    console.error(err);
    state.episodes = [];
    renderError(
      els.episodes,
      `Não foi possível carregar os episódios: ${errText(err)}`,
      () => loadEpisodes(result, season)
    );
    setStatus("Falha ao carregar os episódios.", { error: true });
  }
}

function renderSeasons() {
  if (state.seasons.length < 2) {
    els.seasonWrap.hidden = true;
    return;
  }
  clear(els.season);
  for (const s of state.seasons) {
    const opt = document.createElement("option");
    opt.value = s;
    opt.textContent = /^\d+$/.test(s) ? `Temporada ${s}` : s;
    els.season.appendChild(opt);
  }
  els.season.value = state.season;
  els.seasonWrap.hidden = false;
}

// thumbFor picks the AniList still for an episode, falling back to the
// series poster. The poster now comes from GetEpisodeArt, which resolves it
// through AniList when the scraper gave no image — without that, episodes
// from most sources rendered blank.
function thumbFor(ep) {
  const { thumbs, poster } = state.episodeArt;
  const stripped = String(ep.num || ep.number || "").replace(/^0+/, "");
  return (
    thumbs[ep.number] ||
    thumbs[String(ep.num)] ||
    thumbs[stripped] ||
    poster ||
    ""
  );
}

function renderEpisodes(eps) {
  clear(els.episodes);
  const posterSrc = artworkURL(state.episodeArt.poster || "");

  for (const ep of eps) {
    const li = document.createElement("li");
    li.className = "episode";
    if (state.watched.has(episodeKey(ep))) li.classList.add("watched");

    const main = document.createElement("button");
    main.type = "button";
    main.className = "episode-main";

    const img = document.createElement("img");
    img.className = "episode-thumb";
    img.loading = "lazy";
    img.alt = "";
    // Compare the attribute, not img.src: the latter reads back as an
    // absolute URL, which would never equal the relative /img path and
    // would leave the fallback retrying the poster forever.
    img.onerror = () => {
      if (posterSrc && img.getAttribute("src") !== posterSrc) {
        img.src = posterSrc;
      } else {
        img.style.visibility = "hidden";
      }
    };
    const t = thumbFor(ep);
    if (t) img.src = artworkURL(t);
    main.appendChild(img);

    const info = document.createElement("div");
    info.className = "episode-info";

    const label = document.createElement("span");
    label.className = "episode-label";
    label.textContent = `Ep. ${ep.number || ep.num}`;
    info.appendChild(label);

    if (ep.title) {
      const sub = document.createElement("span");
      sub.className = "episode-sub";
      sub.textContent = ep.title;
      info.appendChild(sub);
    }

    // Air date, when the source reports one (SuperFlix always does).
    const aired = formatAired(ep.aired);
    if (aired) {
      const when = document.createElement("span");
      when.className = "episode-aired";
      when.textContent = aired;
      info.appendChild(when);
    }

    main.appendChild(info);
    main.title = ep.title || label.textContent;
    main.addEventListener("click", () => onEpisodeChosen(li, ep));
    li.appendChild(main);

    if (state.watched.has(episodeKey(ep))) {
      const tag = document.createElement("span");
      tag.className = "watched-tag";
      tag.textContent = "assistido";
      li.appendChild(tag);
    }

    // Secondary action: resolve the stream without launching anything, so
    // the URL can be pasted into another player or a bug report.
    const actions = document.createElement("div");
    actions.className = "episode-actions";
    const copyBtn = document.createElement("button");
    copyBtn.type = "button";
    copyBtn.className = "icon-btn";
    copyBtn.textContent = "Copiar link";
    copyBtn.title =
      "Obter o link do vídeo e copiar para a área de transferência";
    copyBtn.addEventListener("click", (e) => {
      e.stopPropagation();
      copyStreamURL(li, ep);
    });
    actions.appendChild(copyBtn);
    li.appendChild(actions);

    els.episodes.appendChild(li);
  }

  applyEpisodeFilter();
}

export function applyEpisodeFilter() {
  const term = els.episodeFilter.value.trim().toLowerCase();
  const items = /** @type {NodeListOf<HTMLElement>} */ (
    els.episodes.querySelectorAll(".episode")
  );
  items.forEach((li, i) => {
    const ep = state.episodes[i];
    if (!ep) return;
    const hay = `${ep.number} ${ep.num} ${ep.title || ""}`.toLowerCase();
    li.hidden = term !== "" && !hay.includes(term);
  });
}

/** @type {((value: string | null) => void) | null} */
let episodeModalResolve = null;

// openEpisodeModal asks what to do with an episode and at which quality.
// Resolves to "play" | "download" | null (cancelled).
function openEpisodeModal(ep) {
  els.episodeModalTitle.textContent = `Episódio ${ep.number || ep.num}`;
  els.episodeModalSub.textContent = ep.title || state.result?.name || "";
  els.quality.value = state.quality;
  els.actPlay.textContent = state.watched.has(episodeKey(ep))
    ? "Assistir de novo"
    : "Assistir";
  els.episodeModal.hidden = false;
  els.actPlay.focus();

  return new Promise((resolve) => {
    episodeModalResolve = resolve;
  });
}

export function closeEpisodeModal(choice) {
  els.episodeModal.hidden = true;
  const resolve = episodeModalResolve;
  episodeModalResolve = null;
  if (resolve) resolve(choice ?? null);
}

async function onEpisodeChosen(li, ep) {
  if (!state.result) return;

  const choice = await openEpisodeModal(ep);
  if (!choice) return;

  state.quality = els.quality.value;
  writeStorage(STORAGE.quality, state.quality);

  if (choice === "play") await playEpisode(li, ep);
  else await downloadEpisode(ep);
}
