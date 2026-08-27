import { app, errText } from "./bridge.js";
import { state } from "./state.js";
import { clear, els, plural, relativeTime, toast } from "./dom.js";
import { buildCard, enrichCards, loadFavoriteKeys, newPending } from "./cards.js";

export async function loadFavorites() {
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

export async function loadHistory() {
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
        removeTitle: "Remover do histórico",
        onRemove: () => forgetTitle(h),
      })
    );
  }
  enrichCards(pending);
}

// forgetTitle drops one title from the history. A card stands for a title
// rather than a single episode — RecentlyWatched collapses them — so this
// removes every episode of it, which is what the row was showing.
//
// It is not confirmed, matching the Clear-history button next to it: the
// undo is watching the episode again, and a dialog for every deletion would
// be in the way of the case this exists for.
async function forgetTitle(entry) {
  try {
    await app().ForgetTitle(entry.key);
    toast(`“${entry.result.name}” saiu do histórico`);
  } catch (err) {
    console.error(err);
    toast(`Não foi possível remover do histórico: ${errText(err)}`);
    return;
  }
  await loadHistory();
}

function setTabCount(el, n) {
  el.textContent = n ? String(n) : "";
  el.hidden = !n;
}

// refreshLibraryViews re-runs whichever of the two library tabs has already
// been loaded, after something changed the library. A tab never opened stays
// untouched — it will load current data when it is first shown.
export function refreshLibraryViews() {
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

// primeTabCounts fills the Favoritos and Histórico badges without rendering
// either tab, so the numbers are there from the first paint.
export async function primeTabCounts() {
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
