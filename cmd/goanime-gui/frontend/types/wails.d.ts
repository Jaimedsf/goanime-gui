// Types for the Go bridge, mirroring internal/guiapi.
//
// The frontend reaches Go through `window.go.main.App`, injected by the
// Wails runtime at load time. Wails can generate these declarations into
// frontend/src, but that directory is gitignored and needs the wails CLI,
// so CI would have nothing to check against. This file is the committed
// equivalent — hand-written, and therefore the one thing here that can
// drift from the Go source. See scripts/check-bridge.mjs, which fails the
// build when a method exists on one side and not the other.
//
// Nothing in this directory is embedded: main.go embeds `all:frontend/dist`
// only, so these declarations never reach the binary.

// Every bound Go method returns a Promise, including the ones whose Go
// signature is synchronous — the call crosses a process boundary. A Go
// `(T, error)` pair becomes a promise that rejects with the error text.

/** Go: time.Time, marshalled as RFC 3339. */
type GoTime = string;

interface SourceInfo {
  id: string;
  label: string;
  language: string;
  browserGated: boolean;
  seasoned: boolean;
}

interface SearchResult {
  name: string;
  url: string;
  imageURL: string;
  source: string;
  year: string;
  /** "anime" | "movie" | "tv" */
  mediaType: string;
}

interface EpisodeResult {
  number: string;
  num: number;
  url: string;
  title: string;
  dataID: string;
  seasonID: string;
  aired: string;
  isFiller: boolean;
}

interface EpisodeList {
  seasons: string[] | null;
  season: string;
  episodes: EpisodeResult[] | null;
}

interface TitleInfo {
  releaseDate: string;
  releaseLabel: string;
  year: string;
  format: string;
  status: string;
  episodeCount: number;
  score: number;
}

interface EpisodeArt {
  poster: string;
  thumbs: Record<string, string>;
}

interface BrowseQuery {
  mode: string;
  year: number;
  season: string;
  genre: string;
  format: string;
  page: number;
}

interface BrowseItem {
  anilistID: number;
  title: string;
  romaji: string;
  english: string;
  cover: string;
  format: string;
  status: string;
  episodeCount: number;
  score: number;
  genres: string[] | null;
  releaseDate: string;
  releaseLabel: string;
  season: string;
  year: number;
}

interface BrowsePage {
  query: BrowseQuery;
  label: string;
  page: number;
  hasNextPage: boolean;
  items: BrowseItem[] | null;
}

interface Option {
  value: string;
  label: string;
}

/** Go aliases SeasonOption and GenreOption to Option. */
type SeasonOption = Option;
type GenreOption = Option;
type QualityOption = Option;

interface ScheduleEntry {
  anilistID: number;
  title: string;
  romaji: string;
  english: string;
  cover: string;
  episode: number;
  /** Broadcast instant in Unix seconds. */
  airingAt: number;
  /** The same moment as "HH:MM" in the user's timezone. */
  time: string;
  format: string;
  status: string;
  favorite: boolean;
  aired: boolean;
}

interface ScheduleDay {
  date: string;
  weekday: string;
  label: string;
  dateLabel: string;
  today: boolean;
  past: boolean;
  relative: boolean;
  favoriteCount: number;
  entries: ScheduleEntry[] | null;
}

interface WeekSchedule {
  days: ScheduleDay[] | null;
  total: number;
  favoriteTotal: number;
  fetchedAt: GoTime;
  partial: boolean;
  /** Served from the cache past its TTL; refresh behind it. */
  stale: boolean;
}

interface GateStatus {
  source: string;
  gated: boolean;
  ready: boolean;
  setupPending: boolean;
  noDisplay: boolean;
  message: string;
}

interface GateOptions {
  headless: boolean;
  bundled: boolean;
  channel: string;
}

interface FavoriteItem {
  result: SearchResult;
  addedAt: GoTime;
}

interface HistoryEntry {
  key: string;
  result: SearchResult;
  episodeNumber: string;
  episodeNum: number;
  episodeTitle: string;
  seasonID: string;
  watchedAt: GoTime;
}

interface DownloadProgress {
  id: string;
  title: string;
  state: string;
  percent: number;
  received: number;
  total: number;
  path: string;
  error: string;
}

/**
 * The bound Go App. Method names and shapes must match cmd/goanime-gui/app.go.
 */
interface GoApp {
  // sources and search
  Sources(): Promise<SourceInfo[] | null>;
  Search(query: string, source: string): Promise<SearchResult[] | null>;
  CancelSearch(): Promise<void>;

  // episodes
  GetEpisodes(r: SearchResult): Promise<EpisodeList>;
  GetSeasonEpisodes(r: SearchResult, season: string): Promise<EpisodeList>;

  // artwork and metadata
  GetCover(title: string): Promise<string>;
  GetEpisodeThumbnails(title: string): Promise<Record<string, string>>;
  GetEpisodeArt(r: SearchResult): Promise<EpisodeArt>;
  GetTitleInfo(r: SearchResult): Promise<TitleInfo>;

  // catalog
  Browse(q: BrowseQuery): Promise<BrowsePage>;
  SearchTitles(
    item: BrowseItem,
    source: string,
  ): Promise<SearchResult[] | null>;
  ModeOptions(): Promise<Option[] | null>;
  GenreOptions(): Promise<GenreOption[] | null>;
  FormatOptions(): Promise<Option[] | null>;
  CurrentSeasonYear(): Promise<number>;
  CurrentSeasonName(): Promise<string>;
  SeasonOptions(): Promise<SeasonOption[] | null>;
  YearOptions(): Promise<number[] | null>;

  // schedule
  Schedule(): Promise<WeekSchedule>;
  RefreshSchedule(): Promise<WeekSchedule>;
  SearchScheduleEntry(
    e: ScheduleEntry,
    source: string,
  ): Promise<SearchResult[] | null>;

  // source gate
  GetGateStatus(source: string): Promise<GateStatus>;
  PrepareSource(source: string): Promise<GateStatus>;
  GetGateOptions(): Promise<GateOptions>;
  SetGateOptions(opts: GateOptions): Promise<void>;

  // library
  Favorites(): Promise<FavoriteItem[] | null>;
  ToggleFavorite(r: SearchResult): Promise<boolean>;
  IsFavorite(r: SearchResult): Promise<boolean>;
  FavoriteKeys(): Promise<string[] | null>;
  TitleKey(r: SearchResult): Promise<string>;
  RecentlyWatched(n: number): Promise<HistoryEntry[] | null>;
  History(): Promise<HistoryEntry[] | null>;
  WatchedEpisodes(r: SearchResult): Promise<string[] | null>;
  ForgetTitle(key: string): Promise<void>;
  ClearHistory(): Promise<void>;
  LibraryPath(): Promise<string>;

  // playback
  Qualities(): Promise<QualityOption[] | null>;
  ResolveStreamURL(r: SearchResult, ep: EpisodeResult): Promise<string>;
  PlayEpisode(
    r: SearchResult,
    ep: EpisodeResult,
    playerPath: string,
    quality: string,
  ): Promise<void>;
  PlayerAvailable(name: string): Promise<boolean>;
  SourceNeedsBrowser(source: string): Promise<boolean>;
  PickPlayer(): Promise<string>;

  // downloads
  StartDownload(
    r: SearchResult,
    ep: EpisodeResult,
    quality: string,
  ): Promise<string>;
  DownloadStatus(): Promise<DownloadProgress[] | null>;
  CancelDownload(id: string): Promise<boolean>;
  ClearFinishedDownloads(): Promise<void>;
  DownloadsFolder(): Promise<string>;
  OpenDownloadsFolder(): Promise<void>;

  // misc
  CopyToClipboard(text: string): Promise<void>;
}

/**
 * The Wails runtime, injected at load time. Only the parts the frontend
 * actually reaches are declared.
 */
interface WailsRuntime {
  // The payload is whatever the Go side emitted, so it arrives untyped;
  // every listener here validates it before use.
  EventsOn?(event: string, callback: (...data: any[]) => void): () => void;
  EventsOff?(event: string, ...extra: string[]): void;
}

interface Window {
  go?: { main?: { App?: GoApp } };
  runtime?: WailsRuntime;
  /** Assigned on purpose by main.js for debugging from the devtools. */
  __debug?: unknown;
}
