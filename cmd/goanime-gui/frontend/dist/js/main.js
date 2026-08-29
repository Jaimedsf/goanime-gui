// GoAnime GUI frontend. Talks to the Go backend through the Wails runtime
// bindings injected at window.go.main.App.* — every exported method on
// cmd/goanime-gui/app.go:App is callable from here.

import { app, errText } from "./bridge.js";
import { STORAGE, readStorage, state, writeStorage } from "./state.js";
import { clear, els, setStatus, toast } from "./dom.js";
import { goBack, registerTab, showTab } from "./views.js";
import { loadQualities, loadSources } from "./sources.js";
import { toggleFavorite } from "./cards.js";
import {
  closePlayerModal,
  openPlayerModal,
  playerLabel,
  refreshPlayerChip,
  setPlayer,
} from "./playback.js";
import { loadDownloads, openDrawer, wireDownloadEvents } from "./downloads.js";
import {
  applyEpisodeFilter,
  closeEpisodeModal,
  loadEpisodes,
  openResult,
  paintGate,
} from "./episodes.js";
import { openGateSettings, saveGateSettings } from "./gate.js";
import {
  loadFavorites,
  loadHistory,
  primeTabCounts,
  refreshLibraryViews,
} from "./library.js";
import { loadSchedule, renderSchedule } from "./schedule.js";
import {
  applyCatalogView,
  initCatalog,
  loadCatalog,
  syncCatalogControls,
} from "./catalog.js";
import {
  applyResultsView,
  loadQueryHistory,
  runSearch,
  setSearching,
} from "./search.js";
import { hooks } from "./hooks.js";

// --- wiring the modules together -----------------------------------------

// The tabs and the two hooks are connected here, in the one module that is
// allowed to know about all of them. Every other module imports strictly
// downwards, which is what keeps the import graph free of cycles.

registerTab("schedule", () => els.schedulePane, loadSchedule);
registerTab("catalog", () => els.catalogPane, initCatalog);
registerTab("favorites", () => els.favoritesPane, loadFavorites);
registerTab("history", () => els.historyPane, loadHistory);

hooks.openTitle = openResult;

hooks.libraryChanged = () => {
  refreshLibraryViews();
  // The calendar's highlight comes from the backend's own name matching, so
  // it has to be re-fetched rather than patched here. The week is cached on
  // the Go side, so this costs no network call.
  if (state.tabsLoaded.has("schedule")) loadSchedule();
};

// --- event wiring --------------------------------------------------------

els.homeBtn.addEventListener("click", () => {
  state.result = null;
  showTab("schedule");
});

// One listener on the bar rather than five on the buttons, so the tab set
// stays a matter of markup.
els.tabs.addEventListener("click", (e) => {
  const btn = /** @type {HTMLElement | null} */ (
    /** @type {Element} */ (e.target).closest(".tab")
  );
  if (!btn) return;
  state.result = null;
  showTab(btn.dataset.tab);
  setStatus("");
});

// Left/right arrows move between tabs, which is what a tablist is expected
// to do once one of its buttons has focus.
els.tabs.addEventListener("keydown", (e) => {
  if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
  // :not([hidden]) so arrowing can never land on a tab the user cannot see.
  const btns = /** @type {HTMLElement[]} */ ([
    ...els.tabs.querySelectorAll(".tab:not([hidden])"),
  ]);
  const i = btns.indexOf(/** @type {HTMLElement} */ (document.activeElement));
  if (i < 0) return;
  e.preventDefault();
  const next =
    btns[(i + (e.key === "ArrowRight" ? 1 : -1) + btns.length) % btns.length];
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

// Modules have their own scope, so `state` and `els` are no longer reachable
// from the console the way they were when this was one script. This is the
// only door left open — for the devtools and for the CDP harness that drives
// the UI in tests.
window.__debug = { state, els };
