import { state } from "./state.js";
import { els, setStatus } from "./dom.js";

// TABS maps a tab id to its panel and to the loader that fills it. `load` is
// called the first time the tab is opened and never again on its own — a tab
// whose data can go stale re-runs it from the action that changed the data
// (toggling a favorite, clearing the history) rather than on every visit.
//
// Search results are deliberately absent: the header's search box is the only
// way to them, so they open over the tabs the way the episode list does.
// The entries are registered from main.js rather than imported here: a tab
// module imported by views.js, while that module imports showResults back
// from views.js, is a cycle. Registration keeps the arrow pointing one way.
const TABS = {};

// registerTab wires one tab into the bar. `pane` is a getter rather than the
// element itself so the lookup happens when the tab is shown, exactly as the
// inline entries used to do.
export function registerTab(id, pane, load) {
  TABS[id] = { pane, load };
}

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

export function showTab(id) {
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
export function showResults() {
  hidePanes();
  els.resultsPane.hidden = false;
  state.view = "results";
  markTabs(null);
}

// showEpisodes opens the drill-down over whatever was on screen, remembering
// what that was so "Voltar" returns there instead of guessing.
export function showEpisodes(title) {
  els.episodesTitle.textContent = title || "Episódios";
  if (state.view !== "episodes") state.returnTo = state.view;
  hidePanes();
  els.episodesPane.hidden = false;
  state.view = "episodes";
  markTabs(null);
}

// goBack leaves an overlay for whatever is underneath it.
export function goBack() {
  if (state.view === "episodes" && state.returnTo === "results") showResults();
  else showTab(state.tab);
  setStatus("");
}
