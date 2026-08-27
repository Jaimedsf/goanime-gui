import { app, errText, withTimeout } from "./bridge.js";
import { state } from "./state.js";
import { clear, els, plural, renderError } from "./dom.js";
import { setArtwork } from "./cards.js";
import { searchByTitle } from "./search.js";

export async function loadSchedule({ force = false } = {}) {
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

export function renderSchedule() {
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
