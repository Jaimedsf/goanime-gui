import { app } from "./bridge.js";
import { STORAGE, readStorage, state } from "./state.js";
import { clear, els } from "./dom.js";

export async function loadSources() {
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
export function sourceInfo(name) {
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

export async function loadQualities() {
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
