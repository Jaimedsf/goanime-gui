// Local to this module: els below is the only lookup surface the rest of
// the frontend uses, so exporting this would invite bypassing it.
//
// getElementById is typed as possibly null, and every caller here would
// have to answer for that null. They do not need to: check-frontend.mjs
// fails the build when any id below is missing from index.html, which is a
// stronger guarantee than a runtime guard, and it catches the problem at
// `npm run check` rather than when a user clicks the thing. So the cast is
// the check talking, not a shrug.
/** @type {(id: string) => HTMLElement} */
const $ = (id) => /** @type {HTMLElement} */ (document.getElementById(id));

// The form controls need their specific element types, because the rest of
// the frontend reads .value and .checked off them. Which ids are controls
// is worth stating here anyway — it is otherwise only visible in the HTML.
/** @type {(id: string) => HTMLSelectElement} */
const $sel = (id) =>
  /** @type {HTMLSelectElement} */ (document.getElementById(id));
/** @type {(id: string) => HTMLInputElement} */
const $input = (id) =>
  /** @type {HTMLInputElement} */ (document.getElementById(id));
// Only the buttons something disables need this; the rest are plain
// elements as far as this file is concerned.
/** @type {(id: string) => HTMLButtonElement} */
const $btn = (id) =>
  /** @type {HTMLButtonElement} */ (document.getElementById(id));

export const els = {
  homeBtn: $("home-btn"),
  form: $("search-form"),
  query: $input("query"),
  history: $("search-history"),
  source: $sel("source"),
  searchBtn: $btn("search-btn"),
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
  catalogMode: $sel("catalog-mode"),
  catalogSeasonWrap: $("catalog-season-wrap"),
  catalogSeason: $sel("catalog-season"),
  catalogYear: $sel("catalog-year"),
  catalogGenre: $sel("catalog-genre"),
  catalogFormat: $sel("catalog-format"),
  catalogNow: $("catalog-now"),
  catalogReset: $("catalog-reset"),
  catalogFilter: $input("catalog-filter"),
  catalogPrev: $btn("catalog-prev"),
  catalogNext: $btn("catalog-next"),
  catalogPage: $("catalog-page"),

  scheduleWeek: $("schedule-week"),
  scheduleCount: $("schedule-count"),
  scheduleNote: $("schedule-note"),
  scheduleRefresh: $btn("schedule-refresh"),
  scheduleFavsOnly: $input("schedule-favs-only"),

  resultsPane: $("results-pane"),
  backToTab: $("back-to-tab"),
  results: $("results"),
  resultsCount: $("results-count"),
  resultsEmpty: $("results-empty"),
  // Reached by id rather than by querySelector(".empty-title"): the class is
  // also assigned at runtime by renderError, so a class-based lookup here
  // could not be checked against the markup. These can.
  resultsEmptyTitle: $("results-empty-title"),
  resultsEmptySub: $("results-empty-sub"),
  resultsToolbar: $("results-toolbar"),
  resultsFilter: $input("results-filter"),
  resultsSort: $sel("results-sort"),
  filterCount: $("filter-count"),

  episodesPane: $("episodes-pane"),
  episodes: $("episodes"),
  episodesTitle: $("episodes-title"),
  episodesCount: $("episodes-count"),
  episodeFilter: $input("episode-filter"),
  favToggle: $("fav-toggle"),
  seasonWrap: $("season-wrap"),
  season: $sel("season"),
  titleMeta: $("title-meta"),
  gateNote: $("gate-note"),
  gateMessage: $("gate-message"),
  gateCheck: $("gate-check"),
  gateSettings: $("gate-settings"),
  back: $("back-to-results"),

  gateModal: $("gate-modal"),
  gateBundled: $input("gate-bundled"),
  gateHeadless: $input("gate-headless"),
  gateChannel: $sel("gate-channel"),
  gateSave: $("gate-save"),
  gateCancel: $("gate-cancel"),

  episodeModal: $("episode-modal"),
  episodeModalTitle: $("episode-modal-title"),
  episodeModalSub: $("episode-modal-sub"),
  quality: $sel("quality"),
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

export function setStatus(msg, { error = false, busy = false } = {}) {
  els.status.textContent = "";
  els.status.classList.toggle("error", error);
  if (busy) {
    const s = document.createElement("span");
    s.className = "spinner";
    els.status.appendChild(s);
  }
  if (msg) els.status.appendChild(document.createTextNode(msg));
}

/** @type {ReturnType<typeof setTimeout> | undefined} */
let toastTimer;

export function toast(msg) {
  els.toast.textContent = msg;
  els.toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    els.toast.hidden = true;
  }, 2600);
}

export function clear(el) {
  while (el.firstChild) el.removeChild(el.firstChild);
}

export function plural(n, word) {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

export function humanBytes(n) {
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
export function relativeTime(iso) {
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
export function formatAired(raw) {
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
export function episodeKey(ep) {
  return ep.seasonID ? `${ep.seasonID}:${ep.number}` : ep.number;
}

export function skeletons(container, count, extraClass) {
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

export function fillOptions(select, options) {
  clear(select);
  for (const o of options || []) {
    const opt = document.createElement("option");
    opt.value = o.value;
    opt.textContent = o.label;
    select.appendChild(opt);
  }
}

// renderError replaces a grid with an inline failure state plus a retry
// button, so a transient scraper error does not dead-end the UI.
export function renderError(container, message, retry) {
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
