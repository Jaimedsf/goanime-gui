import { app, errText } from "./bridge.js";
import { STORAGE, readStorage, state, writeStorage } from "./state.js";
import {
  clear,
  els,
  plural,
  renderError,
  setStatus,
  skeletons,
} from "./dom.js";
import { showResults } from "./views.js";
import { buildCard, enrichCards, newPending } from "./cards.js";

// searchByTitle drives the "AniList entry to real sources" flow shared by the
// catalog and the weekly calendar: open the results pane, run the caller's
// search, and report what came back. `run` is a thunk rather than a query
// string because each caller has its own binding — the two must stay a single
// code path, or one of them quietly stops merging title variants.
export async function searchByTitle(title, queryText, run) {
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

export function setSearching(on) {
  state.searching = on;
  els.searchBtn.disabled = on;
  els.searchBtn.textContent = on ? "Buscando…" : "Buscar";
  els.cancelBtn.hidden = !on;
}

export async function runSearch(query, source) {
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

export function loadQueryHistory() {
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

// applyResultsView filters by name and sorts, then re-renders. Both run
// client-side over the results already fetched, so typing is instant and
// costs no extra scraping.
export function applyResultsView() {
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

function renderResults(results) {
  clear(els.results);
  const pending = newPending();
  for (const r of results) {
    els.results.appendChild(buildCard(r, pending));
  }
  enrichCards(pending);
}
