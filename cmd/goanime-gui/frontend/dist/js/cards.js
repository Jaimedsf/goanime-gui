import { app, errText, runBounded } from "./bridge.js";
import { state } from "./state.js";
import { els, toast } from "./dom.js";
import { sourceInfo } from "./sources.js";
import { hooks } from "./hooks.js";

export async function loadFavoriteKeys() {
  try {
    state.favoriteKeys = new Set((await app().FavoriteKeys()) || []);
  } catch (err) {
    console.warn("FavoriteKeys() failed", err);
  }
}

export async function toggleFavorite(result, starBtn) {
  try {
    const now = await app().ToggleFavorite(result);
    const key = await app().TitleKey(result);
    if (now) state.favoriteKeys.add(key);
    else state.favoriteKeys.delete(key);

    if (starBtn) paintStar(starBtn, now);
    toast(now ? "Adicionado aos favoritos" : "Removido dos favoritos");

    // Whatever else must react to the library changing is wired in main.js:
    // a card has no business knowing the calendar exists.
    hooks.libraryChanged();

    if (state.result && (await app().TitleKey(state.result)) === key) {
      paintStar(els.favToggle, now);
    }
  } catch (err) {
    console.error(err);
    toast(`Não foi possível atualizar os favoritos: ${errText(err)}`);
  }
}

export function paintStar(btn, on) {
  btn.textContent = on ? "★" : "☆";
  btn.setAttribute("aria-pressed", String(!!on));
  btn.title = on ? "Remover dos favoritos" : "Adicionar aos favoritos";
}

// setArtwork points an <img> at a URL and handles it not arriving.
//
// A calendar week is ~120 covers requested at once, and AniList's image host
// drops some of them under that burst — the URLs are fine, the connection is
// not. So a failure is retried once after a beat, and only a second failure
// falls back to a lettered tile. Hiding the image (what this used to do) left
// a blank gap that looked like missing data rather than a slow load.
export function setArtwork(img, url, title) {
  if (!url) {
    showArtworkFallback(img, title);
    return;
  }

  const src = artworkURL(url);
  let retried = false;
  img.onerror = () => {
    if (!retried) {
      retried = true;
      setTimeout(() => {
        // A cache-busting suffix would defeat the browser cache for every
        // later paint; the same URL is enough for a dropped connection.
        img.src = src;
      }, 1200);
      return;
    }
    showArtworkFallback(img, title);
  };
  img.src = src;
}

// artworkURL routes remote artwork through the Go side, which keeps the
// bytes in a cache of its own under the user's cache directory. Going
// straight to the CDN left that entirely to the webview's HTTP cache — a
// store the app does not configure and cannot stop from being evicted,
// which is why covers came down again on every launch.
//
// Anything that is not an absolute http(s) URL is returned untouched: data:
// URIs and the app's own assets have nothing to gain from the round trip.
export function artworkURL(url) {
  if (!url || !/^https?:\/\//i.test(url)) return url;
  return `/img?u=${encodeURIComponent(url)}`;
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

// buildCard renders one title tile, shared by the results grid, the
// favorites row and the continue-watching row.
export function buildCard(r, pending, opts = {}) {
  const pendingCovers = pending.covers;
  const pendingDates = pending.dates;

  const li = document.createElement("li");
  li.className = "card-wrap";

  const card = document.createElement("button");
  card.type = "button";
  card.className = "card";
  card.addEventListener("click", () => hooks.openTitle(r));

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

  // An optional remove button, used by the history so a single title can be
  // dropped without clearing everything. It sits opposite the star for the
  // same reason the star is out here: neither can nest inside the card
  // button. Cards that pass no handler get no button at all.
  if (opts.onRemove) {
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "star-btn card-remove";
    remove.textContent = "✕";
    remove.title = opts.removeTitle || "Remover";
    remove.setAttribute("aria-label", remove.title);
    remove.addEventListener("click", (e) => {
      e.stopPropagation();
      opts.onRemove(r);
    });
    li.appendChild(remove);
  }

  return li;
}

// isFavoriteResult checks the cached key set. The key format mirrors
// guiapi.titleKey; when a result has no URL the backend falls back to a
// normalised name, which cannot be reproduced faithfully here, so those
// cards start unstarred and correct themselves after a toggle.
export function isFavoriteResult(r) {
  const src = (r.source || "").toLowerCase();
  if (r.url) return state.favoriteKeys.has(`${src}|${r.url.toLowerCase()}`);
  return false;
}

export function newPending() {
  return { covers: [], dates: [] };
}

// enrichCards fills in the artwork and release dates the scrapers did not
// provide. Both come from the same cached AniList lookup, and both run with
// bounded concurrency so a 40-result page does not fire 40 simultaneous
// requests at a rate-limited API.
export function enrichCards(pending) {
  runBounded(pending.covers, 4, async ({ img, title }) => {
    setArtwork(img, await app().GetCover(title), title);
  });

  runBounded(pending.dates, 4, async ({ el, result }) => {
    const info = await app().GetTitleInfo(result);
    if (info && info.releaseLabel) el.textContent = info.releaseLabel;
  });
}
