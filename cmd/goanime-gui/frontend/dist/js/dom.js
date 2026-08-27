export const $ = (id) => document.getElementById(id);

export const els = {
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

let toastTimer = null;

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
