export const STORAGE = {
  player: "goanime.player",
  source: "goanime.source",
  history: "goanime.history",
  quality: "goanime.quality",
  scheduleFavsOnly: "goanime.schedule.favsOnly",
};

export const state = {
  result: null, // currently open SearchResult
  allResults: [], // unfiltered search results
  episodes: [],
  seasons: [],
  season: "",
  episodeArt: { poster: "", thumbs: {} },
  sources: [],
  qualities: [],
  favoriteKeys: new Set(),
  watched: new Set(), // episode keys watched for the open title
  player: readStorage(STORAGE.player, ""),
  quality: readStorage(STORAGE.quality, "best"),
  downloads: new Map(),
  catalog: { items: [], query: null, page: 1, hasNext: false },
  tab: "schedule", // active tab id
  // Which of the three layers is on screen: a tab, the search results, or the
  // episode list. returnTo is what the episode list was opened from.
  view: "tab",
  returnTo: "tab",
  // Tabs load their contents the first time they are opened. AniList is
  // rate-limited, and loading all five at boot is what used to earn a 429.
  tabsLoaded: new Set(),
  schedule: null, // last WeekSchedule, or null before the first load
  // Bumped per schedule load so a slow first fetch cannot overwrite the
  // result of the refresh that followed it.
  scheduleSeq: 0,
  searching: false,
  // Bumped on every search so a cancelled or superseded run cannot
  // overwrite the status text of the one that replaced it.
  searchSeq: 0,
};

export function readStorage(key, fallback) {
  try {
    const v = localStorage.getItem(key);
    return v === null ? fallback : v;
  } catch {
    return fallback;
  }
}

export function writeStorage(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* private mode or blocked storage — preferences just won't persist */
  }
}
