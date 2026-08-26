// GoAnime GUI frontend. Talks to the Go backend through the Wails runtime
// bindings injected at window.go.main.App.* — every exported method on
// cmd/goanime-gui/app.go:App is callable from here.

const $ = (id) => document.getElementById(id);

const els = {
  homeBtn: $("home-btn"),
  form: $("search-form"),
  query: $("query"),
  history: $("search-history"),
  source: $("source"),
  searchBtn: $("search-btn"),
  cancelBtn: $("cancel-btn"),
  playerChip: $("player-chip"),
  playerName: $("player-name"),
  downloadsChip: $("downloads-chip"),
  downloadsCount: $("downloads-count"),
  status: $("status"),

  tabs: $("tabs"),
  schedulePane: $("schedule-pane"),
  catalogPane: $("catalog-pane"),
  favoritesPane: $("favorites-pane"),
  historyPane: $("history-pane"),
  tabFavoritesCount: $("tab-favorites-count"),
  tabHistoryCount: $("tab-history-count"),

  historyList: $("history"),
  historyCount: $("history-count"),
  historyEmpty: $("history-empty"),
  clearHistory: $("clear-history"),
  favorites: $("favorites"),
  favoritesCount: $("favorites-count"),
  favoritesEmpty: $("favorites-empty"),

  catalog: $("catalog"),
  catalogCount: $("catalog-count"),
  catalogHeading: $("catalog-heading"),
  catalogMode: $("catalog-mode"),
  catalogSeasonWrap: $("catalog-season-wrap"),
  catalogSeason: $("catalog-season"),
  catalogYear: $("catalog-year"),
  catalogGenre: $("catalog-genre"),
  catalogFormat: $("catalog-format"),
  catalogNow: $("catalog-now"),
  catalogReset: $("catalog-reset"),
  catalogFilter: $("catalog-filter"),
  catalogPrev: $("catalog-prev"),
  catalogNext: $("catalog-next"),
  catalogPage: $("catalog-page"),

  scheduleWeek: $("schedule-week"),
  scheduleCount: $("schedule-count"),
  scheduleNote: $("schedule-note"),
  scheduleRefresh: $("schedule-refresh"),
  scheduleFavsOnly: $("schedule-favs-only"),

  resultsPane: $("results-pane"),
  backToTab: $("back-to-tab"),
  results: $("results"),
  resultsCount: $("results-count"),
  resultsEmpty: $("results-empty"),
  resultsToolbar: $("results-toolbar"),
  resultsFilter: $("results-filter"),
  resultsSort: $("results-sort"),
  filterCount: $("filter-count"),

  episodesPane: $("episodes-pane"),
  episodes: $("episodes"),
  episodesTitle: $("episodes-title"),
  episodesCount: $("episodes-count"),
  episodeFilter: $("episode-filter"),
  favToggle: $("fav-toggle"),
  seasonWrap: $("season-wrap"),
  season: $("season"),
  titleMeta: $("title-meta"),
  gateNote: $("gate-note"),
  gateMessage: $("gate-message"),
  gateCheck: $("gate-check"),
  gateSettings: $("gate-settings"),
  back: $("back-to-results"),

  gateModal: $("gate-modal"),
  gateBundled: $("gate-bundled"),
  gateHeadless: $("gate-headless"),
  gateChannel: $("gate-channel"),
  gateSave: $("gate-save"),
  gateCancel: $("gate-cancel"),

  episodeModal: $("episode-modal"),
  episodeModalTitle: $("episode-modal-title"),
  episodeModalSub: $("episode-modal-sub"),
  quality: $("quality"),
  actPlay: $("act-play"),
  actDownload: $("act-download"),
  episodeModalCancel: $("episode-modal-cancel"),

  playerModal: $("player-modal"),
  choices: $("player-choices"),
  playerModalCancel: $("player-modal-cancel"),

  drawer: $("downloads-drawer"),
  drawerClose: $("drawer-close"),
  downloadsList: $("downloads-list"),
  downloadsEmpty: $("downloads-empty"),
  downloadsPath: $("downloads-path"),
  openFolder: $("open-folder"),
  clearFinished: $("clear-finished"),

  toast: $("toast"),
};

const STORAGE = {
  player: "goanime.player",
  source: "goanime.source",
  history: "goanime.history",
  quality: "goanime.quality",
  scheduleFavsOnly: "goanime.schedule.favsOnly",
};

// Built-in player options. `id` is what the backend receives; an empty id
// means mpv through the CLI's IPC launcher (full header support).
const PLAYERS = [
  { id: "", label: "mpv", note: "recomendado" },
  { id: "vlc", label: "VLC" },
  { id: "mpc-hc", label: "MPC-HC" },
  { id: "wmplayer", label: "Windows Media Player" },
];

const state = {
  result: null, // currently open SearchResult
  allResults: [], // unfiltered search results
  episodes: [],
  seasons: [],
  season: "",
  episodeArt: { poster: "", thumbs: {} },
  sources: [],
  qualities: [],
  favoriteKeys: new Set(),
  watched: new Set(), // episode keys watched for the open title
  player: readStorage(STORAGE.player, ""),
  quality: readStorage(STORAGE.quality, "best"),
  downloads: new Map(),
  catalog: { items: [], query: null, page: 1, hasNext: false },
  tab: "schedule", // active tab id
  // Which of the three layers is on screen: a tab, the search results, or the
  // episode list. returnTo is what the episode list was opened from.
  view: "tab",
  returnTo: "tab",
  // Tabs load their contents the first time they are opened. AniList is
  // rate-limited, and loading all five at boot is what used to earn a 429.
  tabsLoaded: new Set(),
  schedule: null, // last WeekSchedule, or null before the first load
  // Bumped per schedule load so a slow first fetch cannot overwrite the
  // result of the refresh that followed it.
  scheduleSeq: 0,
  searching: false,
  // Bumped on every search so a cancelled or superseded run cannot
  // overwrite the status text of the one that replaced it.
  searchSeq: 0,
};

// --- small helpers -------------------------------------------------------

function readStorage(key, fallback) {
  try {
    const v = localStorage.getItem(key);
    return v === null ? fallback : v;
  } catch {
    return fallback;
  }
}

function writeStorage(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* private mode or blocked storage — preferences just won't persist */
  }
}

function app() {
  if (!window.go || !window.go.main || !window.go.main.App) {
    throw new Error(
      "Ponte com o Wails indisponível — rode o executável ou `wails dev`."
    );
  }
  return window.go.main.App;
}

// errText unwraps the many shapes a rejected Wails call can take so the
// user sees the Go error text rather than "[object Object]".
function errText(err) {
  if (!err) return "erro desconhecido";
  if (typeof err === "string") return err;
  return err.message || String(err);
}

function setStatus(msg, { error = false, busy = false } = {}) {
  els.status.textContent = "";
  els.status.classList.toggle("error", error);
  if (busy) {
    const s = document.createElement("span");
    s.className = "spinner";
    els.status.appendChild(s);
  }
  if (msg) els.status.appendChild(document.createTextNode(msg));
}

let toastTimer = null;
function toast(msg) {
  els.toast.textContent = msg;
  els.toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    els.toast.hidden = true;
  }, 2600);
}

function clear(el) {
  while (el.firstChild) el.removeChild(el.firstChild);
}

function plural(n, word) {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

function humanBytes(n) {
  if (!n || n < 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
}

// relativeTime turns a timestamp into "2h ago" style text for the history.
function relativeTime(iso) {
  const then = new Date(iso).getTime();
  if (!then) return "";
  const mins = Math.floor((Date.now() - then) / 60000);
  if (mins < 1) return "agora mesmo";
  if (mins < 60) return `há ${mins} min`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `há ${hours} h`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `há ${days} d`;
  return new Date(then).toLocaleDateString("pt-BR");
}

// formatAired renders a source's air date. The sources hand back plain
// "YYYY-MM-DD" strings; anything else is passed through untouched rather
// than guessed at, and an unparseable value renders nothing.
function formatAired(raw) {
  if (!raw) return "";
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(raw.trim());
  if (!m) return raw.trim();

  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  if (Number.isNaN(d.getTime())) return raw.trim();

  return d.toLocaleDateString("pt-BR", {
    day: "numeric",
    month: "short",
    year: "numeric",
  });
}

// episodeKey mirrors guiapi.episodeKey so watched marks line up.
function episodeKey(ep) {
  return ep.seasonID ? `${ep.seasonID}:${ep.number}` : ep.number;
}

// --- view switching ------------------------------------------------------

// TABS maps a tab id to its panel and to the loader that fills it. `load` is
// called the first time the tab is opened and never again on its own — a tab
// whose data can go stale re-runs it from the action that changed the data
// (toggling a favorite, clearing the history) rather than on every visit.
//
// Search results are deliberately absent: the header's search box is the only
// way to them, so they open over the tabs the way the episode list does.
const TABS = {
  schedule: { pane: () => els.schedulePane, load: () => loadSchedule() },
  catalog: { pane: () => els.catalogPane, load: () => initCatalog() },
  favorites: { pane: () => els.favoritesPane, load: () => loadFavorites() },
  history: { pane: () => els.historyPane, load: () => loadHistory() },
};

// hidePanes clears every view, so each show* function only has to reveal its
// own and cannot leave two on screen at once.
function hidePanes() {
  for (const spec of Object.values(TABS)) spec.pane().hidden = true;
  els.resultsPane.hidden = true;
  els.episodesPane.hidden = true;
}

// markTabs highlights the active tab, or none while an overlay (results,
// episodes) is covering them.
function markTabs(id) {
  for (const btn of els.tabs.querySelectorAll(".tab")) {
    btn.setAttribute("aria-selected", String(btn.dataset.tab === id));
  }
}

function showTab(id) {
  if (!TABS[id]) id = "schedule";
  state.tab = id;
  state.view = "tab";

  hidePanes();
  TABS[id].pane().hidden = false;
  markTabs(id);
  loadTabOnce(id);
}

// loadTabOnce runs a tab's loader the first time it is opened.
//
// The `catch` is the point. A loader that fails clears the mark, so opening
// the tab again retries it. Marking a tab loaded up front and never clearing
// it meant one transient AniList hiccup left that tab blank for the rest of
// the session with no way to recover — clicking it again did nothing at all.
async function loadTabOnce(id) {
  const spec = TABS[id];
  if (!spec.load || state.tabsLoaded.has(id)) return;

  // Marked before awaiting, so a second click while the first load is still
  // in flight does not fire it twice.
  state.tabsLoaded.add(id);

  let ok = false;
  try {
    ok = (await spec.load()) !== false;
  } catch (err) {
    console.error(`a aba "${id}" falhou ao carregar`, err);
  }
  if (!ok) state.tabsLoaded.delete(id);
}

// showResults opens the search results over the tabs. They are reached only
// from the header's search box, so no tab stays highlighted.
function showResults() {
  hidePanes();
  els.resultsPane.hidden = false;
  state.view = "results";
  markTabs(null);
}

// showEpisodes opens the drill-down over whatever was on screen, remembering
// what that was so "Voltar" returns there instead of guessing.
function showEpisodes(title) {
  els.episodesTitle.textContent = title || "Episódios";
  if (state.view !== "episodes") state.returnTo = state.view;
  hidePanes();
  els.episodesPane.hidden = false;
  state.view = "episodes";
  markTabs(null);
}

// goBack leaves an overlay for whatever is underneath it.
function goBack() {
  if (state.view === "episodes" && state.returnTo === "results") showResults();
  else showTab(state.tab);
  setStatus("");
}

// withTimeout gives up on a bridge call that never settles. Without it such a
// call leaves a skeleton on screen with no error and no way out — which is
// exactly what "carregamento infinito" looks like from the outside.
function withTimeout(promise, ms, what) {
  let timer;
  return Promise.race([
    promise.finally(() => clearTimeout(timer)),
    new Promise((_, reject) => {
      timer = setTimeout(
        () =>
          reject(
            new Error(`${what} não respondeu em ${Math.round(ms / 1000)}s`)
          ),
        ms
      );
    }),
  ]);
}

function skeletons(container, count, extraClass) {
  clear(container);
  for (let i = 0; i < count; i++) {
    const li = document.createElement("li");
    li.className = `skeleton ${extraClass || ""}`.trim();
    const img = document.createElement("div");
    img.className = "sk-img skeleton-box";
    const l1 = document.createElement("div");
    l1.className = "sk-line skeleton-box";
    const l2 = document.createElement("div");
    l2.className = "sk-line short skeleton-box";
    li.append(img, l1, l2);
    container.appendChild(li);
  }
}

// --- sources and qualities ----------------------------------------------

async function loadSources() {
  let sources;
  try {
    sources = await app().Sources();
  } catch (err) {
    console.warn("Sources() failed, keeping the default option", err);
    return;
  }
  state.sources = sources || [];

  clear(els.source);
  for (const s of state.sources) {
    const opt = document.createElement("option");
    opt.value = s.id;
    opt.textContent = s.language ? `${s.label} · ${s.language}` : s.label;
    els.source.appendChild(opt);
  }

  const saved = readStorage(STORAGE.source, "all");
  if (state.sources.some((s) => s.id === saved)) els.source.value = saved;
}

// sourceInfo looks up a SourceInfo by the display name the scraper stamps
// on a result ("Animefire.io" vs the registry kind "AnimeFire"), so the
// match is by prefix in either direction.
function sourceInfo(name) {
  if (!name) return null;
  const n = name.toLowerCase();
  return (
    state.sources.find((s) => s.label.toLowerCase() === n) ||
    state.sources.find(
      (s) =>
        s.id !== "all" &&
        s.id !== "ptbr" &&
        (n.startsWith(s.id) || s.id.startsWith(n.split(".")[0]))
    ) ||
    null
  );
}

async function loadQualities() {
  try {
    state.qualities = (await app().Qualities()) || [];
  } catch (err) {
    console.warn("Qualities() failed, using a static fallback", err);
    state.qualities = [{ value: "best", label: "Best available" }];
  }
  clear(els.quality);
  for (const q of state.qualities) {
    const opt = document.createElement("option");
    opt.value = q.value;
    opt.textContent = q.label;
    els.quality.appendChild(opt);
  }
  if (state.qualities.some((q) => q.value === state.quality)) {
    els.quality.value = state.quality;
  }
}

// --- favorites and history ----------------------------------------------

async function loadFavoriteKeys() {
  try {
    state.favoriteKeys = new Set((await app().FavoriteKeys()) || []);
  } catch (err) {
    console.warn("FavoriteKeys() failed", err);
  }
}

async function toggleFavorite(result, starBtn) {
  try {
    const now = await app().ToggleFavorite(result);
    const key = await app().TitleKey(result);
    if (now) state.favoriteKeys.add(key);
    else state.favoriteKeys.delete(key);

    if (starBtn) paintStar(starBtn, now);
    toast(now ? "Adicionado aos favoritos" : "Removido dos favoritos");

    refreshLibraryViews();
    // The calendar's highlight comes from the backend's own name matching,
    // so it has to be re-fetched rather than patched here. The week is
    // cached on the Go side, so this costs no network call.
    if (state.tabsLoaded.has("schedule")) loadSchedule();

    if (state.result && (await app().TitleKey(state.result)) === key) {
      paintStar(els.favToggle, now);
    }
  } catch (err) {
    console.error(err);
    toast(`Não foi possível atualizar os favoritos: ${errText(err)}`);
  }
}

function paintStar(btn, on) {
  btn.textContent = on ? "★" : "☆";
  btn.setAttribute("aria-pressed", String(!!on));
  btn.title = on ? "Remover dos favoritos" : "Adicionar aos favoritos";
}

async function loadFavorites() {
  await loadFavoriteKeys();

  let favorites = [];
  try {
    favorites = (await app().Favorites()) || [];
  } catch (err) {
    console.warn("Favorites() failed", err);
  }

  els.favoritesCount.textContent = favorites.length
    ? plural(favorites.length, "título")
    : "";
  els.favoritesEmpty.hidden = favorites.length > 0;
  setTabCount(els.tabFavoritesCount, favorites.length);

  clear(els.favorites);
  const pending = newPending();
  for (const f of favorites) {
    els.favorites.appendChild(buildCard(f.result, pending));
  }
  enrichCards(pending);
}

async function loadHistory() {
  await loadFavoriteKeys();

  let history = [];
  try {
    history = (await app().RecentlyWatched(24)) || [];
  } catch (err) {
    console.warn("RecentlyWatched() failed", err);
  }

  els.historyCount.textContent = history.length
    ? plural(history.length, "título")
    : "";
  els.historyEmpty.hidden = history.length > 0;
  els.clearHistory.hidden = history.length === 0;
  setTabCount(els.tabHistoryCount, history.length);

  clear(els.historyList);
  const pending = newPending();
  for (const h of history) {
    els.historyList.appendChild(
      buildCard(h.result, pending, {
        subtitle: h.seasonID
          ? `S${h.seasonID}E${h.episodeNumber} · ${relativeTime(h.watchedAt)}`
          : `Ep. ${h.episodeNumber} · ${relativeTime(h.watchedAt)}`,
        progress: "Continuar",
      })
    );
  }
  enrichCards(pending);
}

function setTabCount(el, n) {
  el.textContent = n ? String(n) : "";
  el.hidden = !n;
}

// refreshLibraryViews re-runs whichever of the two library tabs has already
// been loaded, after something changed the library. A tab never opened stays
// untouched — it will load current data when it is first shown.
function refreshLibraryViews() {
  if (state.tabsLoaded.has("favorites")) loadFavorites();
  if (state.tabsLoaded.has("history")) loadHistory();
}

// --- weekly airing calendar ----------------------------------------------

// The calendar is AniList's airing schedule for the seven days starting
// today. Like the catalog, an entry is metadata rather than a playable item:
// clicking one searches every source for the title.
//
// Favorites are matched by name on the Go side — AniList and the scrapers
// share no identifier — and arrive already flagged, so nothing here has to
// guess which airing is one of the user's.

async function loadSchedule({ force = false } = {}) {
  const seq = ++state.scheduleSeq;
  const current = () => seq === state.scheduleSeq;

  if (!state.schedule || force) skeletonWeek();
  els.scheduleRefresh.disabled = true;

  try {
    // A forced refresh refetches every page, and the AniList gate spaces
    // those out, so the ceiling is generous — but it is a ceiling.
    const week = await withTimeout(
      force ? app().RefreshSchedule() : app().Schedule(),
      90000,
      "O calendário"
    );
    if (!current()) return;
    state.schedule = week;
    renderSchedule();
    return true;
  } catch (err) {
    if (!current()) return;
    console.warn("Schedule() failed", err);
    state.schedule = null;
    els.scheduleCount.textContent = "";
    els.scheduleNote.textContent = "";
    renderError(
      els.scheduleWeek,
      `Não foi possível carregar o calendário: ${errText(err)}`,
      () => loadSchedule({ force: true })
    );
    return false;
  } finally {
    // Unconditionally, not only for the current run: a superseded load that
    // left the button disabled was one of the ways the calendar got stuck
    // looking like it was still working.
    els.scheduleRefresh.disabled = false;
  }
}

// skeletonWeek draws seven empty columns so the section keeps its height
// while the week loads, instead of the catalog jumping up and back down.
function skeletonWeek() {
  clear(els.scheduleWeek);
  for (let i = 0; i < 7; i++) {
    const day = document.createElement("div");
    day.className = "day";
    const head = document.createElement("div");
    head.className = "day-head";
    const line = document.createElement("div");
    line.className = "skeleton-box";
    head.appendChild(line);
    day.appendChild(head);

    const list = document.createElement("div");
    list.className = "day-list";
    for (let j = 0; j < 4; j++) {
      const row = document.createElement("div");
      row.className = "skeleton-box";
      list.appendChild(row);
    }
    day.appendChild(list);
    els.scheduleWeek.appendChild(day);
  }
}

function renderSchedule() {
  const week = state.schedule;
  if (!week || !week.days) return;

  const favsOnly = els.scheduleFavsOnly.checked;

  els.scheduleCount.textContent = week.favoriteTotal
    ? `${plural(week.total, "episódio")} · ${week.favoriteTotal} nos favoritos`
    : plural(week.total, "episódio");

  // The filter is only honest when there is something to filter to: with no
  // favorites at all it would blank the calendar and look broken, so say why
  // instead.
  if (favsOnly && week.favoriteTotal === 0) {
    els.scheduleNote.textContent =
      "Nenhum favorito estreia esta semana — mostrando tudo.";
  } else if (week.partial) {
    els.scheduleNote.textContent =
      "Semana cheia: alguns títulos do fim da semana podem não aparecer.";
  } else {
    els.scheduleNote.textContent = "";
  }

  const filter = favsOnly && week.favoriteTotal > 0;

  clear(els.scheduleWeek);
  for (const day of week.days) {
    els.scheduleWeek.appendChild(buildScheduleDay(day, filter));
  }
}

function buildScheduleDay(day, favsOnly) {
  const col = document.createElement("div");
  col.className = "day";
  if (day.today) col.classList.add("today");
  else if (day.past) col.classList.add("past");

  const head = document.createElement("div");
  head.className = "day-head";

  const label = document.createElement("span");
  label.className = "day-label";
  label.textContent = day.label;
  head.appendChild(label);

  const date = document.createElement("span");
  date.className = "day-date";
  // "Ontem"/"Hoje"/"Amanhã" say nothing about which weekday they fall on, so
  // those columns carry it alongside the date. The rest already have the
  // weekday as their label.
  date.textContent = day.relative
    ? `${day.weekday}, ${day.dateLabel}`
    : day.dateLabel;
  head.appendChild(date);

  if (day.favoriteCount) {
    const fav = document.createElement("span");
    fav.className = "day-fav";
    fav.textContent = `★ ${day.favoriteCount}`;
    fav.title = plural(day.favoriteCount, "favorito");
    head.appendChild(fav);
  }

  col.appendChild(head);

  const entries = (day.entries || []).filter((e) => !favsOnly || e.favorite);

  if (!entries.length) {
    const empty = document.createElement("p");
    empty.className = "day-empty";
    empty.textContent = favsOnly
      ? "Nenhum favorito"
      : day.past
        ? "Nada saiu"
        : "Nada previsto";
    col.appendChild(empty);
    return col;
  }

  const list = document.createElement("ul");
  list.className = "day-list";
  for (const entry of entries) list.appendChild(buildAiring(entry));
  col.appendChild(list);

  return col;
}

// setArtwork points an <img> at a URL and handles it not arriving.
//
// A calendar week is ~120 covers requested at once, and AniList's image host
// drops some of them under that burst — the URLs are fine, the connection is
// not. So a failure is retried once after a beat, and only a second failure
// falls back to a lettered tile. Hiding the image (what this used to do) left
// a blank gap that looked like missing data rather than a slow load.
function setArtwork(img, url, title) {
  if (!url) {
    showArtworkFallback(img, title);
    return;
  }

  let retried = false;
  img.onerror = () => {
    if (!retried) {
      retried = true;
      setTimeout(() => {
        // A cache-busting suffix would defeat the browser cache for every
        // later paint; the same URL is enough for a dropped connection.
        img.src = url;
      }, 1200);
      return;
    }
    showArtworkFallback(img, title);
  };
  img.src = url;
}

// showArtworkFallback replaces a failed image with the title's first letter,
// so the row still reads as a title rather than as empty space.
function showArtworkFallback(img, title) {
  img.onerror = null;
  img.removeAttribute("src");
  img.hidden = true;

  if (img.nextElementSibling?.classList.contains("art-fallback")) return;

  const box = document.createElement("span");
  box.className = `art-fallback ${img.className}`;
  box.setAttribute("aria-hidden", "true");
  box.textContent = (title || "?").trim().charAt(0).toUpperCase() || "?";
  img.after(box);
}

function buildAiring(entry) {
  const li = document.createElement("li");

  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "airing";
  if (entry.favorite) btn.classList.add("fav");
  if (entry.aired) btn.classList.add("past");
  btn.title = `Procurar “${entry.title}” em todas as fontes`;
  btn.addEventListener("click", () => openScheduleEntry(entry));

  const img = document.createElement("img");
  img.className = "airing-thumb";
  img.loading = "lazy";
  img.alt = "";
  btn.appendChild(img);
  setArtwork(img, entry.cover, entry.title);

  const body = document.createElement("div");
  body.className = "airing-body";

  const title = document.createElement("div");
  title.className = "airing-title";
  title.textContent = entry.title;
  body.appendChild(title);

  const meta = document.createElement("div");
  meta.className = "airing-meta";
  const bits = [];
  if (entry.favorite) bits.push("★");
  if (entry.episode) bits.push(`Ep. ${entry.episode}`);
  if (entry.time) bits.push(entry.time);
  meta.textContent = bits.join(" · ");
  body.appendChild(meta);

  btn.appendChild(body);
  li.appendChild(btn);
  return li;
}

// openScheduleEntry runs the same all-sources, all-title-variants search a
// catalog click runs, so the two paths cannot drift apart.
async function openScheduleEntry(entry) {
  await searchByTitle(entry.title, entry.romaji || entry.title, () =>
    app().SearchScheduleEntry(entry, "all")
  );
}

// --- seasonal catalog ----------------------------------------------------

// The catalog lists what AniList says aired in a given season. Entries are
// metadata, not playable items: clicking one runs a normal search for its
// title, so the user sees which sources actually carry it.

async function initCatalog() {
  try {
    const [modes, years, seasons, genres, formats, year, season] =
      await Promise.all([
        app().ModeOptions(),
        app().YearOptions(),
        app().SeasonOptions(),
        app().GenreOptions(),
        app().FormatOptions(),
        app().CurrentSeasonYear(),
        app().CurrentSeasonName(),
      ]);

    fillOptions(els.catalogMode, modes);
    fillOptions(els.catalogSeason, seasons);
    fillOptions(els.catalogGenre, genres);
    fillOptions(els.catalogFormat, formats);

    // The year picker carries an "any year" entry for the modes that are
    // not pinned to a single season.
    clear(els.catalogYear);
    const any = document.createElement("option");
    any.value = "0";
    any.textContent = "Qualquer ano";
    els.catalogYear.appendChild(any);
    for (const y of years || []) {
      const opt = document.createElement("option");
      opt.value = String(y);
      opt.textContent = String(y);
      els.catalogYear.appendChild(opt);
    }

    els.catalogMode.value = "season";
    els.catalogSeason.value = season;
    els.catalogYear.value = String(year);
  } catch (err) {
    // The pickers failing is not a reason to show an empty tab. The backend
    // normalises an empty query into "the season airing now", so the grid can
    // still be filled; only the dropdowns are missing.
    console.warn("catalog pickers failed to load", err);
    setStatus(`Os filtros do catálogo não carregaram: ${errText(err)}`, {
      error: true,
    });
  }

  syncCatalogControls();
  return loadCatalog(1);
}

function fillOptions(select, options) {
  clear(select);
  for (const o of options || []) {
    const opt = document.createElement("option");
    opt.value = o.value;
    opt.textContent = o.label;
    select.appendChild(opt);
  }
}

// syncCatalogControls hides the season picker outside season mode, where it
// has no effect, and forces a concrete year in season mode, where "any
// year" is not a valid combination.
function syncCatalogControls() {
  const isSeason = els.catalogMode.value === "season";
  els.catalogSeasonWrap.hidden = !isSeason;

  if (isSeason && els.catalogYear.value === "0") {
    return app()
      .CurrentSeasonYear()
      .then((y) => {
        els.catalogYear.value = String(y);
      })
      .catch(() => {});
  }
  return Promise.resolve();
}

function currentCatalogQuery(page) {
  const mode = els.catalogMode.value;
  return {
    mode,
    year: parseInt(els.catalogYear.value, 10) || 0,
    season: mode === "season" ? els.catalogSeason.value : "",
    genre: els.catalogGenre.value,
    format: els.catalogFormat.value,
    page,
  };
}

async function loadCatalog(page) {
  const query = currentCatalogQuery(page);

  skeletons(els.catalog, 12);
  els.catalogCount.textContent = "";
  els.catalogPrev.disabled = true;
  els.catalogNext.disabled = true;

  try {
    const result = await withTimeout(app().Browse(query), 60000, "O catálogo");

    state.catalog = {
      items: result.items || [],
      query: result.query,
      page: result.page,
      hasNext: !!result.hasNextPage,
    };

    els.catalogHeading.textContent = result.label || "Lançamentos";
    els.catalogPage.textContent = `Página ${result.page}`;
    els.catalogPrev.disabled = result.page <= 1;
    els.catalogNext.disabled = !result.hasNextPage;

    applyCatalogView();
    return true;
  } catch (err) {
    console.error(err);
    state.catalog.items = [];
    renderError(
      els.catalog,
      `Não foi possível carregar o catálogo: ${errText(err)}`,
      () => loadCatalog(page)
    );
    els.catalogPage.textContent = "";
    return false;
  }
}

// applyCatalogView filters the loaded page by name. It is client-side, so
// it narrows the current page only — the label says as much.
function applyCatalogView() {
  const term = els.catalogFilter.value.trim().toLowerCase();
  let view = state.catalog.items;

  if (term) {
    view = view.filter((it) =>
      [it.title, it.romaji, it.english]
        .filter(Boolean)
        .some((t) => t.toLowerCase().includes(term))
    );
  }

  els.catalogCount.textContent = term
    ? `${view.length} de ${state.catalog.items.length} nesta página`
    : plural(state.catalog.items.length, "título");

  if (!view.length) {
    renderError(
      els.catalog,
      term
        ? `Nenhum título desta página corresponde a “${term}”.`
        : "Nenhum título com esses filtros.",
      null
    );
    return;
  }

  clear(els.catalog);
  for (const item of view) els.catalog.appendChild(buildCatalogCard(item));
}

// GENRE_LABELS mirrors the Go map so catalog cards read in Portuguese
// without a round-trip per genre. An unknown genre passes through
// untranslated rather than vanishing.
const GENRE_LABELS = {
  Action: "Ação",
  Adventure: "Aventura",
  Comedy: "Comédia",
  Drama: "Drama",
  Ecchi: "Ecchi",
  Fantasy: "Fantasia",
  Horror: "Terror",
  "Mahou Shoujo": "Garota mágica",
  Mecha: "Mecha",
  Music: "Música",
  Mystery: "Mistério",
  Psychological: "Psicológico",
  Romance: "Romance",
  "Sci-Fi": "Ficção científica",
  "Slice of Life": "Slice of life",
  Sports: "Esportes",
  Supernatural: "Sobrenatural",
  Thriller: "Suspense",
};

function genreLabel(name) {
  return GENRE_LABELS[name] || name;
}

function buildCatalogCard(item) {
  const li = document.createElement("li");
  li.className = "card-wrap";

  const card = document.createElement("button");
  card.type = "button";
  card.className = "card";
  card.title = `Procurar “${item.title}” em todas as fontes`;
  card.addEventListener("click", () => openCatalogItem(item));

  if (item.score) {
    const score = document.createElement("span");
    score.className = "card-score";
    score.textContent = `${item.score}%`;
    li.appendChild(score);
  }

  const img = document.createElement("img");
  img.className = "card-image";
  img.loading = "lazy";
  img.alt = "";
  card.appendChild(img);
  setArtwork(img, item.cover, item.title);

  const body = document.createElement("div");
  body.className = "card-body";

  const title = document.createElement("p");
  title.className = "card-title";
  title.textContent = item.title;
  body.appendChild(title);

  // The English name is what the PT-BR sources usually list under, so
  // showing it makes the search that follows less surprising.
  if (item.english && item.english !== item.title) {
    const alt = document.createElement("span");
    alt.className = "card-sub";
    alt.textContent = item.english;
    body.appendChild(alt);
  }

  if (item.releaseLabel) {
    const date = document.createElement("span");
    date.className = "card-date";
    date.textContent = item.releaseLabel;
    body.appendChild(date);
  }

  const badges = document.createElement("div");
  badges.className = "badges";

  if (item.format) {
    const f = document.createElement("span");
    f.className = "badge";
    f.textContent = FORMAT_LABELS[item.format] || item.format;
    badges.appendChild(f);
  }
  if (item.status) {
    const s = document.createElement("span");
    s.className = "badge";
    s.textContent = STATUS_LABELS[item.status] || item.status;
    badges.appendChild(s);
  }
  if (item.episodeCount) {
    const e = document.createElement("span");
    e.className = "badge";
    e.textContent = plural(item.episodeCount, "ep");
    badges.appendChild(e);
  }
  body.appendChild(badges);

  if (item.genres && item.genres.length) {
    const g = document.createElement("span");
    g.className = "card-genres";
    g.textContent = item.genres.slice(0, 3).map(genreLabel).join(" · ");
    body.appendChild(g);
  }

  card.appendChild(body);
  li.appendChild(card);
  return li;
}

// openCatalogItem searches every source for a catalog entry.
//
// Two details matter here. It searches "all" rather than the header
// dropdown: that dropdown persists between sessions, so a user who once
// picked AllAnime would get English-only results from every catalog click,
// with no hint why. And it goes through SearchTitles, which tries the
// romaji, English and display names together — the PT-BR sources often
// index the localised name, so a romaji-only query can miss them.
async function openCatalogItem(item) {
  await searchByTitle(item.title, item.romaji || item.title, () =>
    app().SearchTitles(item, "all")
  );
}

// searchByTitle drives the "AniList entry to real sources" flow shared by the
// catalog and the weekly calendar: open the results pane, run the caller's
// search, and report what came back. `run` is a thunk rather than a query
// string because each caller has its own binding — the two must stay a single
// code path, or one of them quietly stops merging title variants.
async function searchByTitle(title, queryText, run) {
  const seq = ++state.searchSeq;
  const current = () => seq === state.searchSeq;

  showResults();
  els.query.value = queryText;
  els.resultsEmpty.hidden = true;
  els.resultsToolbar.hidden = true;
  els.resultsCount.textContent = "";
  els.filterCount.textContent = "";
  skeletons(els.results, 12);
  setStatus(`Procurando “${title}” em todas as fontes…`, { busy: true });
  setSearching(true);

  try {
    const results = (await run()) || [];
    if (!current()) return;

    state.allResults = results;
    els.resultsCount.textContent = results.length
      ? plural(results.length, "resultado")
      : "";

    if (results.length) {
      els.resultsToolbar.hidden = results.length < 6;
      applyResultsView();
      setStatus(`${plural(results.length, "resultado")} para “${title}”.`);
    } else {
      clear(els.results);
      els.resultsEmpty.hidden = false;
      els.resultsEmpty.querySelector(".empty-title").textContent =
        "Nenhuma fonte tem este título";
      els.resultsEmpty.querySelector(".empty-sub").textContent =
        "Os dados vêm do AniList, que lista tudo que existe; nem todo título está disponível nas fontes.";
      setStatus("");
    }
  } catch (err) {
    console.error(err);
    if (!current()) return;
    clear(els.results);
    renderError(els.results, `A busca falhou: ${errText(err)}`, () =>
      searchByTitle(title, queryText, run)
    );
    setStatus("A busca falhou.", { error: true });
  } finally {
    if (current()) setSearching(false);
  }
}

// --- search --------------------------------------------------------------

function setSearching(on) {
  state.searching = on;
  els.searchBtn.disabled = on;
  els.searchBtn.textContent = on ? "Buscando…" : "Buscar";
  els.cancelBtn.hidden = !on;
}

async function runSearch(query, source) {
  pushHistory(query);
  writeStorage(STORAGE.source, source);

  const seq = ++state.searchSeq;
  const current = () => seq === state.searchSeq;

  showResults();
  els.resultsEmpty.hidden = true;
  els.resultsToolbar.hidden = true;
  els.resultsCount.textContent = "";
  els.filterCount.textContent = "";
  skeletons(els.results, 12);
  setStatus(`Buscando “${query}”…`, { busy: true });
  setSearching(true);

  try {
    const results = (await app().Search(query, source)) || [];
    if (!current()) return;

    state.allResults = results;
    els.resultsCount.textContent = results.length
      ? plural(results.length, "resultado")
      : "";

    if (results.length) {
      // The filter only earns its space once the list is long enough to
      // be worth narrowing.
      els.resultsToolbar.hidden = results.length < 6;
      applyResultsView();
      setStatus(`${plural(results.length, "resultado")} para “${query}”.`);
    } else {
      clear(els.results);
      els.resultsEmpty.hidden = false;
      els.resultsEmpty.querySelector(".empty-title").textContent =
        "Nenhum resultado";
      els.resultsEmpty.querySelector(".empty-sub").textContent =
        "Tente um termo mais curto, ou mude a fonte para “Todas as fontes”.";
      setStatus("");
    }
  } catch (err) {
    console.error(err);
    if (!current()) return;
    clear(els.results);
    renderError(els.results, `A busca falhou: ${errText(err)}`, () =>
      runSearch(query, source)
    );
    setStatus("A busca falhou.", { error: true });
  } finally {
    if (current()) setSearching(false);
  }
}

// --- search history (the query box, not the watch history) ---------------

function loadQueryHistory() {
  let items = [];
  try {
    items = JSON.parse(readStorage(STORAGE.history, "[]")) || [];
  } catch {
    items = [];
  }
  clear(els.history);
  for (const q of items) {
    const opt = document.createElement("option");
    opt.value = q;
    els.history.appendChild(opt);
  }
  return items;
}

function pushHistory(query) {
  const items = loadQueryHistory().filter((q) => q !== query);
  items.unshift(query);
  writeStorage(STORAGE.history, JSON.stringify(items.slice(0, 12)));
  loadQueryHistory();
}

// renderError replaces a grid with an inline failure state plus a retry
// button, so a transient scraper error does not dead-end the UI.
function renderError(container, message, retry) {
  clear(container);
  const li = document.createElement("li");
  li.className = "empty";
  li.style.gridColumn = "1 / -1";

  const title = document.createElement("p");
  title.className = "empty-title";
  title.textContent = message;
  li.appendChild(title);

  if (retry) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "link";
    btn.textContent = "Tentar de novo";
    btn.addEventListener("click", retry);
    li.appendChild(btn);
  }
  container.appendChild(li);
}

// applyResultsView filters by name and sorts, then re-renders. Both run
// client-side over the results already fetched, so typing is instant and
// costs no extra scraping.
function applyResultsView() {
  const term = els.resultsFilter.value.trim().toLowerCase();
  let view = state.allResults;

  if (term) {
    view = view.filter((r) => (r.name || "").toLowerCase().includes(term));
  }

  const mode = els.resultsSort.value;
  if (mode === "name") {
    view = [...view].sort((a, b) =>
      (a.name || "").localeCompare(b.name || "", undefined, {
        sensitivity: "base",
      })
    );
  } else if (mode === "year") {
    view = [...view].sort(
      (a, b) => (parseInt(b.year, 10) || 0) - (parseInt(a.year, 10) || 0)
    );
  }

  els.filterCount.textContent =
    term || mode !== "relevance"
      ? `mostrando ${view.length} de ${state.allResults.length}`
      : "";

  if (!view.length) {
    renderError(els.results, `Nenhum resultado corresponde a “${term}”.`, null);
    return;
  }
  renderResults(view);
}

// buildCard renders one title tile, shared by the results grid, the
// favorites row and the continue-watching row.
function buildCard(r, pending, opts = {}) {
  const pendingCovers = pending.covers;
  const pendingDates = pending.dates;

  const li = document.createElement("li");
  li.className = "card-wrap";

  const card = document.createElement("button");
  card.type = "button";
  card.className = "card";
  card.addEventListener("click", () => openResult(r));

  const img = document.createElement("img");
  img.className = "card-image";
  img.loading = "lazy";
  img.alt = "";
  card.appendChild(img);
  // A scraper that gave us no artwork leaves the image for enrichCards to
  // fill in from AniList; one that did goes straight through setArtwork.
  if (r.imageURL) setArtwork(img, r.imageURL, r.name);
  else pendingCovers.push({ img, title: r.name });

  const body = document.createElement("div");
  body.className = "card-body";

  const title = document.createElement("p");
  title.className = "card-title";
  title.textContent = r.name;
  body.appendChild(title);

  // Release date: the scraper's year when it has one, otherwise filled in
  // from AniList by the enrichment pass below.
  const date = document.createElement("span");
  date.className = "card-date";
  if (r.year) date.textContent = r.year;
  else pendingDates.push({ el: date, result: r });
  body.appendChild(date);

  if (opts.subtitle) {
    const sub = document.createElement("span");
    sub.className = "card-sub";
    sub.textContent = opts.subtitle;
    body.appendChild(sub);
  }
  if (opts.progress) {
    const p = document.createElement("span");
    p.className = "card-progress";
    p.textContent = opts.progress;
    body.appendChild(p);
  }

  const badges = document.createElement("div");
  badges.className = "badges";

  const src = document.createElement("span");
  src.className = "badge src";
  src.textContent = r.source || "unknown";
  badges.appendChild(src);

  if (r.mediaType && r.mediaType !== "anime") {
    const mt = document.createElement("span");
    mt.className = "badge";
    mt.textContent = r.mediaType;
    badges.appendChild(mt);
  }

  const info = sourceInfo(r.source);

  // Language is the thing a Brazilian user is actually scanning for, so it
  // gets its own badge rather than being implied by the source name.
  if (info && info.language) {
    const lang = document.createElement("span");
    lang.className = info.language === "PT-BR" ? "badge lang-pt" : "badge";
    lang.textContent = info.language;
    badges.appendChild(lang);
  }

  if (info && info.browserGated) {
    const gate = document.createElement("span");
    gate.className = "badge gate";
    gate.textContent = "verificação";
    gate.title =
      "Assistir por esta fonte pode abrir rapidamente uma janela de navegador para passar pela verificação de robô.";
    badges.appendChild(gate);
  }

  body.appendChild(badges);
  card.appendChild(body);
  li.appendChild(card);

  // The star sits outside the card button: a button cannot nest inside
  // another button, so the wrapper carries it.
  const star = document.createElement("button");
  star.type = "button";
  star.className = "star-btn card-star";
  paintStar(star, isFavoriteResult(r));
  star.addEventListener("click", (e) => {
    e.stopPropagation();
    toggleFavorite(r, star);
  });
  li.appendChild(star);

  return li;
}

// isFavoriteResult checks the cached key set. The key format mirrors
// guiapi.titleKey; when a result has no URL the backend falls back to a
// normalised name, which cannot be reproduced faithfully here, so those
// cards start unstarred and correct themselves after a toggle.
function isFavoriteResult(r) {
  const src = (r.source || "").toLowerCase();
  if (r.url) return state.favoriteKeys.has(`${src}|${r.url.toLowerCase()}`);
  return false;
}

function renderResults(results) {
  clear(els.results);
  const pending = newPending();
  for (const r of results) {
    els.results.appendChild(buildCard(r, pending));
  }
  enrichCards(pending);
}

function newPending() {
  return { covers: [], dates: [] };
}

// enrichCards fills in the artwork and release dates the scrapers did not
// provide. Both come from the same cached AniList lookup, and both run with
// bounded concurrency so a 40-result page does not fire 40 simultaneous
// requests at a rate-limited API.
function enrichCards(pending) {
  runBounded(pending.covers, 4, async ({ img, title }) => {
    setArtwork(img, await app().GetCover(title), title);
  });

  runBounded(pending.dates, 4, async ({ el, result }) => {
    const info = await app().GetTitleInfo(result);
    if (info && info.releaseLabel) el.textContent = info.releaseLabel;
  });
}

// runBounded applies fn over items with at most `limit` in flight. Failures
// are logged and skipped: a missing cover or date is cosmetic and must
// never break the grid.
async function runBounded(items, limit, fn) {
  if (!items.length) return;
  let idx = 0;

  async function worker() {
    while (idx < items.length) {
      const item = items[idx++];
      try {
        await fn(item);
      } catch (err) {
        console.warn("card enrichment failed", err);
      }
    }
  }

  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
}

// --- episodes ------------------------------------------------------------

async function openResult(result) {
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

// --- title metadata and the human-check notice ---------------------------

const FORMAT_LABELS = {
  TV: "Série de TV",
  TV_SHORT: "Curta de TV",
  MOVIE: "Filme",
  SPECIAL: "Especial",
  OVA: "OVA",
  ONA: "ONA",
  MUSIC: "Videoclipe",
};

const STATUS_LABELS = {
  RELEASING: "Em exibição",
  FINISHED: "Finalizado",
  NOT_YET_RELEASED: "Não lançado",
  CANCELLED: "Cancelado",
  HIATUS: "Em hiato",
};

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
async function refreshGateNote(result) {
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

function paintGate(st) {
  els.gateNote.className = `note ${st.ready ? "ready" : "blocked"}`;
  els.gateMessage.textContent = st.message || "";
}

// --- helper-browser settings ---------------------------------------------

async function openGateSettings() {
  try {
    const opts = await app().GetGateOptions();
    els.gateBundled.checked = !!opts.bundled;
    els.gateHeadless.checked = !!opts.headless;
    els.gateChannel.value = opts.channel || "";
  } catch (err) {
    console.warn("GetGateOptions failed", err);
  }
  els.gateModal.hidden = false;
  els.gateBundled.focus();
}

async function saveGateSettings() {
  try {
    await app().SetGateOptions({
      bundled: els.gateBundled.checked,
      headless: els.gateHeadless.checked,
      channel: els.gateChannel.value,
    });
    toast("Configuração salva — vale a partir da próxima reprodução");
  } catch (err) {
    toast(`Não foi possível salvar: ${errText(err)}`);
  }
  els.gateModal.hidden = true;
  if (state.result) refreshGateNote(state.result);
}

async function refreshWatched(result) {
  try {
    state.watched = new Set((await app().WatchedEpisodes(result)) || []);
  } catch (err) {
    console.warn("WatchedEpisodes() failed", err);
    state.watched = new Set();
  }
}

async function loadEpisodes(result, season) {
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
    renderError(els.episodes, `Não foi possível carregar os episódios: ${errText(err)}`, () =>
      loadEpisodes(result, season)
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
  const poster = state.episodeArt.poster || "";

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
    img.onerror = () => {
      if (poster && img.src !== poster) img.src = poster;
      else img.style.visibility = "hidden";
    };
    const t = thumbFor(ep);
    if (t) img.src = t;
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
    copyBtn.title = "Obter o link do vídeo e copiar para a área de transferência";
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

function applyEpisodeFilter() {
  const term = els.episodeFilter.value.trim().toLowerCase();
  const items = els.episodes.querySelectorAll(".episode");
  items.forEach((li, i) => {
    const ep = state.episodes[i];
    if (!ep) return;
    const hay = `${ep.number} ${ep.num} ${ep.title || ""}`.toLowerCase();
    li.hidden = term !== "" && !hay.includes(term);
  });
}

// --- episode action modal ------------------------------------------------

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

function closeEpisodeModal(choice) {
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

// --- playback ------------------------------------------------------------

function playerLabel(id) {
  const known = PLAYERS.find((p) => p.id === id);
  if (known) return known.label;
  // A custom path picked from the file dialog: show just the file name.
  return id.split(/[\\/]/).pop() || "mpv";
}

function refreshPlayerChip() {
  els.playerName.textContent = playerLabel(state.player);
}

function setPlayer(id) {
  state.player = id;
  writeStorage(STORAGE.player, id);
  refreshPlayerChip();
}

let playerModalResolve = null;

// openPlayerModal resolves to the chosen player id, or null if cancelled.
async function openPlayerModal() {
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
  els.choices.querySelector("button").focus();

  return new Promise((resolve) => {
    playerModalResolve = resolve;
  });
}

function closePlayerModal(value) {
  els.playerModal.hidden = true;
  const resolve = playerModalResolve;
  playerModalResolve = null;
  if (resolve) resolve(value ?? null);
}

async function playEpisode(li, ep) {
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

async function copyStreamURL(li, ep) {
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
    setStatus(`Não foi possível obter o link: ${errText(err)}`, { error: true });
  } finally {
    li.classList.remove("busy");
  }
}

// --- downloads -----------------------------------------------------------

async function downloadEpisode(ep) {
  try {
    await app().StartDownload(state.result, ep, state.quality);
    toast(`Episódio ${ep.number || ep.num} na fila`);
    openDrawer();
  } catch (err) {
    console.error(err);
    setStatus(`Não foi possível iniciar o download: ${errText(err)}`, { error: true });
  }
}

function activeDownloadCount() {
  let n = 0;
  for (const d of state.downloads.values()) {
    if (d.state === "resolving" || d.state === "downloading") n++;
  }
  return n;
}

function refreshDownloadsChip() {
  const active = activeDownloadCount();
  els.downloadsCount.textContent = active || state.downloads.size || 0;
  els.downloadsCount.classList.toggle("active", active > 0);
}

function renderDownloads() {
  const items = [...state.downloads.values()].sort(
    (a, b) => jobNum(b.id) - jobNum(a.id)
  );

  els.downloadsEmpty.hidden = items.length > 0;
  clear(els.downloadsList);

  for (const d of items) {
    const li = document.createElement("li");
    li.className = `dl ${d.state}`;

    const top = document.createElement("div");
    top.className = "dl-top";

    const title = document.createElement("span");
    title.className = "dl-title";
    title.textContent = d.title;
    top.appendChild(title);

    if (d.state === "resolving" || d.state === "downloading") {
      const cancel = document.createElement("button");
      cancel.type = "button";
      cancel.className = "icon-btn";
      cancel.textContent = "Cancelar";
      cancel.addEventListener("click", () => app().CancelDownload(d.id));
      top.appendChild(cancel);
    }
    li.appendChild(top);

    const bar = document.createElement("div");
    bar.className = "dl-bar";
    const fill = document.createElement("div");
    fill.className = "dl-fill";
    fill.style.width = `${Math.max(0, Math.min(100, d.percent || 0))}%`;
    bar.appendChild(fill);
    li.appendChild(bar);

    const meta = document.createElement("div");
    meta.className = "dl-meta";

    const left = document.createElement("span");
    left.textContent = stateLabel(d);
    meta.appendChild(left);

    const right = document.createElement("span");
    right.textContent = d.total
      ? `${humanBytes(d.received)} / ${humanBytes(d.total)}`
      : humanBytes(d.received);
    meta.appendChild(right);
    li.appendChild(meta);

    if (d.error) {
      const err = document.createElement("div");
      err.className = "dl-error";
      err.textContent = d.error;
      li.appendChild(err);
    }

    els.downloadsList.appendChild(li);
  }

  refreshDownloadsChip();
}

function stateLabel(d) {
  switch (d.state) {
    case "resolving":
      return "Obtendo o link…";
    case "downloading":
      return `${Math.round(d.percent || 0)}%`;
    case "done":
      return "Concluído";
    case "cancelled":
      return "Cancelado";
    case "error":
      return "Falhou";
    default:
      return d.state;
  }
}

function jobNum(id) {
  return parseInt(String(id).replace("job-", ""), 10) || 0;
}

function openDrawer() {
  els.drawer.hidden = false;
  renderDownloads();
}

async function loadDownloads() {
  try {
    const jobs = (await app().DownloadStatus()) || [];
    state.downloads = new Map(jobs.map((j) => [j.id, j]));
  } catch (err) {
    console.warn("DownloadStatus() failed", err);
  }
  try {
    els.downloadsPath.textContent = await app().DownloadsFolder();
  } catch {
    /* path is a nicety, not worth surfacing a failure for */
  }
  renderDownloads();
}

// wireDownloadEvents subscribes to the backend's progress stream. Falls back
// to nothing if the runtime is absent (e.g. the page opened outside Wails).
function wireDownloadEvents() {
  if (!window.runtime || !window.runtime.EventsOn) {
    console.warn("Wails runtime events unavailable; downloads won't live-update");
    return;
  }
  window.runtime.EventsOn("download:progress", (p) => {
    if (!p || !p.id) return;
    state.downloads.set(p.id, p);
    if (!els.drawer.hidden) renderDownloads();
    else refreshDownloadsChip();

    if (p.state === "done") toast(`Baixado: ${p.title}`);
    if (p.state === "error") toast(`Falha no download: ${p.title}`);
  });
}

// --- event wiring --------------------------------------------------------

els.homeBtn.addEventListener("click", () => {
  state.result = null;
  showTab("schedule");
});

// One listener on the bar rather than five on the buttons, so the tab set
// stays a matter of markup.
els.tabs.addEventListener("click", (e) => {
  const btn = e.target.closest(".tab");
  if (!btn) return;
  state.result = null;
  showTab(btn.dataset.tab);
  setStatus("");
});

// Left/right arrows move between tabs, which is what a tablist is expected
// to do once one of its buttons has focus.
els.tabs.addEventListener("keydown", (e) => {
  if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
  const btns = [...els.tabs.querySelectorAll(".tab")];
  const i = btns.indexOf(document.activeElement);
  if (i < 0) return;
  e.preventDefault();
  const next = btns[(i + (e.key === "ArrowRight" ? 1 : -1) + btns.length) % btns.length];
  next.focus();
  showTab(next.dataset.tab);
});

els.form.addEventListener("submit", (e) => {
  e.preventDefault();
  const q = els.query.value.trim();
  if (q) runSearch(q, els.source.value);
});

els.cancelBtn.addEventListener("click", async () => {
  // Retire the in-flight run first so its rejection cannot overwrite the
  // "cancelled" message below.
  state.searchSeq++;
  setSearching(false);
  setStatus("Busca cancelada.");
  clear(els.results);
  els.resultsEmpty.hidden = false;
  try {
    await app().CancelSearch();
  } catch (err) {
    console.warn("CancelSearch failed", err);
  }
});

els.resultsFilter.addEventListener("input", applyResultsView);
els.resultsSort.addEventListener("change", applyResultsView);

// Any filter change restarts at page 1: staying on page 4 of a listing
// that only has two would show an empty grid.
els.scheduleRefresh.addEventListener("click", () =>
  loadSchedule({ force: true })
);

els.scheduleFavsOnly.addEventListener("change", () => {
  writeStorage(
    STORAGE.scheduleFavsOnly,
    els.scheduleFavsOnly.checked ? "1" : "0"
  );
  renderSchedule();
});

els.catalogYear.addEventListener("change", () => loadCatalog(1));
els.catalogSeason.addEventListener("change", () => loadCatalog(1));
els.catalogGenre.addEventListener("change", () => loadCatalog(1));
els.catalogFormat.addEventListener("change", () => loadCatalog(1));
els.catalogFilter.addEventListener("input", applyCatalogView);

els.catalogMode.addEventListener("change", async () => {
  await syncCatalogControls();
  loadCatalog(1);
});

els.catalogNow.addEventListener("click", async () => {
  try {
    els.catalogMode.value = "season";
    els.catalogYear.value = String(await app().CurrentSeasonYear());
    els.catalogSeason.value = await app().CurrentSeasonName();
  } catch (err) {
    console.warn("could not resolve the current season", err);
    return;
  }
  await syncCatalogControls();
  loadCatalog(1);
});

els.catalogReset.addEventListener("click", async () => {
  els.catalogGenre.value = "";
  els.catalogFormat.value = "";
  els.catalogFilter.value = "";
  if (els.catalogMode.value !== "season") els.catalogYear.value = "0";
  await syncCatalogControls();
  loadCatalog(1);
});

els.catalogPrev.addEventListener("click", () => {
  if (state.catalog.page > 1) loadCatalog(state.catalog.page - 1);
});

els.catalogNext.addEventListener("click", () => {
  if (state.catalog.hasNext) loadCatalog(state.catalog.page + 1);
});

els.back.addEventListener("click", () => {
  state.result = null;
  goBack();
});

// The results view has its own way back, since it is no longer a tab.
els.backToTab.addEventListener("click", () => {
  state.result = null;
  showTab(state.tab);
  setStatus("");
});

els.favToggle.addEventListener("click", () => {
  if (state.result) toggleFavorite(state.result, els.favToggle);
});

els.gateCheck.addEventListener("click", async () => {
  if (!state.result) return;
  els.gateMessage.textContent = "Verificando…";
  try {
    const st = await app().PrepareSource(state.result.source);
    paintGate(st);
  } catch (err) {
    // PrepareSource returns the status alongside the error; the message is
    // the useful half, so show that rather than the wrapped Go error.
    els.gateNote.className = "note blocked";
    els.gateMessage.textContent = errText(err);
  }
});

els.gateSettings.addEventListener("click", openGateSettings);
els.gateSave.addEventListener("click", saveGateSettings);
els.gateCancel.addEventListener("click", () => {
  els.gateModal.hidden = true;
});
els.gateModal.addEventListener("click", (e) => {
  if (e.target === els.gateModal) els.gateModal.hidden = true;
});

els.clearHistory.addEventListener("click", async () => {
  try {
    await app().ClearHistory();
    toast("Histórico limpo");
  } catch (err) {
    toast(`Não foi possível limpar o histórico: ${errText(err)}`);
  }
  loadHistory();
});

els.season.addEventListener("change", () => {
  if (state.result) loadEpisodes(state.result, els.season.value);
});

els.episodeFilter.addEventListener("input", applyEpisodeFilter);

els.actPlay.addEventListener("click", () => closeEpisodeModal("play"));
els.actDownload.addEventListener("click", () => closeEpisodeModal("download"));
els.episodeModalCancel.addEventListener("click", () => closeEpisodeModal(null));
els.episodeModal.addEventListener("click", (e) => {
  if (e.target === els.episodeModal) closeEpisodeModal(null);
});

els.playerChip.addEventListener("click", async () => {
  const chosen = await openPlayerModal();
  if (chosen !== null) {
    setPlayer(chosen);
    toast(`Player definido como ${playerLabel(chosen)}`);
  }
});

els.playerModalCancel.addEventListener("click", () => closePlayerModal(null));
els.playerModal.addEventListener("click", (e) => {
  if (e.target === els.playerModal) closePlayerModal(null);
});

els.downloadsChip.addEventListener("click", () => {
  if (els.drawer.hidden) openDrawer();
  else els.drawer.hidden = true;
});

els.drawerClose.addEventListener("click", () => {
  els.drawer.hidden = true;
});

els.openFolder.addEventListener("click", async () => {
  try {
    await app().OpenDownloadsFolder();
  } catch (err) {
    toast(`Não foi possível abrir a pasta: ${errText(err)}`);
  }
});

els.clearFinished.addEventListener("click", async () => {
  try {
    await app().ClearFinishedDownloads();
  } catch (err) {
    console.warn("ClearFinishedDownloads failed", err);
  }
  await loadDownloads();
});

document.addEventListener("keydown", (e) => {
  // Ctrl/Cmd+K or "/" focuses the search box from anywhere.
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
    e.preventDefault();
    els.query.focus();
    els.query.select();
    return;
  }
  if (
    e.key === "/" &&
    document.activeElement !== els.query &&
    document.activeElement !== els.resultsFilter &&
    document.activeElement !== els.episodeFilter &&
    document.activeElement !== els.catalogFilter
  ) {
    e.preventDefault();
    els.query.focus();
    return;
  }
  if (e.key === "Escape") {
    if (!els.gateModal.hidden) els.gateModal.hidden = true;
    else if (!els.episodeModal.hidden) closeEpisodeModal(null);
    else if (!els.playerModal.hidden) closePlayerModal(null);
    else if (!els.drawer.hidden) els.drawer.hidden = true;
    else if (state.view === "episodes") els.back.click();
    else if (state.searching) els.cancelBtn.click();
    else if (state.view === "results") showTab(state.tab);
    else if (state.tab !== "schedule") showTab("schedule");
  }
});

// --- boot ----------------------------------------------------------------

(async function init() {
  refreshPlayerChip();
  loadQueryHistory();
  wireDownloadEvents();
  els.scheduleFavsOnly.checked =
    readStorage(STORAGE.scheduleFavsOnly, "0") === "1";
  await Promise.all([loadSources(), loadQualities(), loadDownloads()]);

  // Only the calendar loads now. The other tabs fetch when first opened,
  // which is what keeps the cold start under AniList's rate limit.
  showTab("schedule");

  // The tab badges are the one thing worth knowing without opening the tab,
  // and both counts are local reads — no AniList call involved.
  primeTabCounts();

  els.query.focus();
})();

// primeTabCounts fills the Favoritos and Histórico badges without rendering
// either tab, so the numbers are there from the first paint.
async function primeTabCounts() {
  try {
    const [favorites, history] = await Promise.all([
      app().Favorites(),
      app().RecentlyWatched(24),
    ]);
    setTabCount(els.tabFavoritesCount, (favorites || []).length);
    setTabCount(els.tabHistoryCount, (history || []).length);
  } catch (err) {
    console.warn("tab counts failed", err);
  }
}
