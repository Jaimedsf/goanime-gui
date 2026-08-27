import { app, errText, withTimeout } from "./bridge.js";
import { FORMAT_LABELS, STATUS_LABELS, genreLabel } from "./labels.js";
import { state } from "./state.js";
import { clear, els, fillOptions, plural, renderError, setStatus, skeletons } from "./dom.js";
import { setArtwork } from "./cards.js";
import { searchByTitle } from "./search.js";

export async function initCatalog() {
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


// syncCatalogControls hides the season picker outside season mode, where it
// has no effect, and forces a concrete year in season mode, where "any
// year" is not a valid combination.
export function syncCatalogControls() {
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

// loadCatalog fetches one page and paints it. It resolves to false when the
// fetch failed, which is what tells views.js to let the tab be retried.
export async function loadCatalog(page) {
  skeletons(els.catalog, 12);
  els.catalogCount.textContent = "";
  els.catalogPrev.disabled = true;
  els.catalogNext.disabled = true;

  try {
    const result = await withTimeout(
      app().Browse(currentCatalogQuery(page)),
      60000,
      "O catálogo"
    );

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
export function applyCatalogView() {
  const term = els.catalogFilter.value.trim().toLowerCase();
  const all = state.catalog.items;
  let view = all;

  if (term) {
    view = view.filter((it) =>
      [it.title, it.romaji, it.english]
        .filter(Boolean)
        .some((t) => t.toLowerCase().includes(term))
    );
  }

  els.catalogCount.textContent = term
    ? `${view.length} de ${all.length} nesta página`
    : plural(all.length, "título");

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
