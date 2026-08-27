package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/alvarorichard/Goanime/internal/guiapi"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// downloadProgressEvent is the event name the frontend listens on for live
// download updates.
const downloadProgressEvent = "download:progress"

// App holds the Wails runtime context and exposes methods to the
// JavaScript frontend. Every exported method on this struct is bound by
// Wails and becomes callable from JS as window.go.main.App.<MethodName>.
type App struct {
	ctx context.Context
}

// NewApp constructs a new App.
func NewApp() *App {
	return &App{}
}

// startup is invoked by Wails once the frontend is ready. It also bridges
// guiapi's download progress into Wails events, which is why guiapi itself
// carries no Wails dependency.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	guiapi.SetProgressHook(func(p guiapi.DownloadProgress) {
		wruntime.EventsEmit(ctx, downloadProgressEvent, p)
	})

	// Wails only sets the small window icon; this fills in the large one
	// used by Alt+Tab. It polls for the window, so it runs detached.
	go applyWindowIcon()
}

// shutdown clears the progress hook so no event is emitted against a dead
// context while the window is closing.
func (a *App) shutdown(_ context.Context) {
	guiapi.SetProgressHook(nil)
}

// --- sources and search --------------------------------------------------

// Sources returns the live source list for the search dropdown. It is
// derived from the source registry, so it never lists a source that was
// removed upstream or switched off via GOANIME_DISABLED_SOURCES.
func (a *App) Sources() []guiapi.SourceInfo {
	return guiapi.Sources()
}

// Search runs a headless search and returns matches.
func (a *App) Search(query, source string) ([]guiapi.SearchResult, error) {
	return guiapi.Search(query, source)
}

// CancelSearch aborts the search currently in flight, if any.
func (a *App) CancelSearch() {
	guiapi.CancelSearch()
}

// --- episodes ------------------------------------------------------------

// GetEpisodes fetches the first season's episodes for a search result,
// along with the full season list when the source has seasons.
func (a *App) GetEpisodes(r guiapi.SearchResult) (guiapi.EpisodeList, error) {
	return guiapi.GetEpisodes(r)
}

// GetSeasonEpisodes fetches a specific season for a seasoned source.
func (a *App) GetSeasonEpisodes(r guiapi.SearchResult, season string) (guiapi.EpisodeList, error) {
	return guiapi.GetSeasonEpisodes(r, season)
}

// --- artwork -------------------------------------------------------------

// GetCover returns a cover-art URL for a title, fetched (and cached) from
// AniList. An empty string means no cover is available.
func (a *App) GetCover(title string) string {
	return guiapi.GetCover(title)
}

// GetEpisodeThumbnails returns episode number to thumbnail URL for a
// title. Always returns a map (possibly empty) so the frontend can treat
// the response uniformly.
func (a *App) GetEpisodeThumbnails(title string) map[string]string {
	return guiapi.GetEpisodeThumbnails(title)
}

// GetEpisodeArt returns the episode thumbnails plus the poster to fall back
// on, in one call.
func (a *App) GetEpisodeArt(r guiapi.SearchResult) guiapi.EpisodeArt {
	return guiapi.GetEpisodeArt(r)
}

// GetTitleInfo returns release metadata (date, format, status, episode
// count) for a title. Empty fields mean "unknown", never an error.
func (a *App) GetTitleInfo(r guiapi.SearchResult) guiapi.TitleInfo {
	return guiapi.GetTitleInfo(r)
}

// --- seasonal catalog ----------------------------------------------------

// Browse returns one page of the catalog. Missing or invalid fields are
// normalised, so a zero-value query means "the season airing now".
func (a *App) Browse(q guiapi.BrowseQuery) (guiapi.BrowsePage, error) {
	return guiapi.Browse(q)
}

// SearchTitles searches every source for a catalog entry, trying the
// romaji, English and display names and merging the hits. A romaji-only
// search returns English sources only, because the PT-BR sources usually
// index the localised name.
func (a *App) SearchTitles(item guiapi.BrowseItem, source string) ([]guiapi.SearchResult, error) {
	return guiapi.SearchTitles(item, source)
}

// ModeOptions returns the catalog listings (season, trending, top, …).
func (a *App) ModeOptions() []guiapi.Option {
	return guiapi.ModeOptions()
}

// GenreOptions returns AniList's genre list, translated where known.
func (a *App) GenreOptions() []guiapi.GenreOption {
	return guiapi.GenreOptions()
}

// FormatOptions returns the media formats (TV, movie, OVA, …).
func (a *App) FormatOptions() []guiapi.Option {
	return guiapi.FormatOptions()
}

// CurrentSeasonYear returns the year of the season airing now.
func (a *App) CurrentSeasonYear() int {
	year, _ := guiapi.CurrentSeason()
	return year
}

// CurrentSeasonName returns the season airing now (WINTER/SPRING/…).
func (a *App) CurrentSeasonName() string {
	_, season := guiapi.CurrentSeason()
	return season
}

// SeasonOptions returns the four seasons for the picker.
func (a *App) SeasonOptions() []guiapi.SeasonOption {
	return guiapi.SeasonOptions()
}

// YearOptions returns the years offered by the picker, newest first.
func (a *App) YearOptions() []int {
	return guiapi.YearOptions()
}

// --- weekly airing calendar ----------------------------------------------

// Schedule returns the seven days ahead of episode airings, with the
// user's favorites marked. The result is cached for a few minutes.
func (a *App) Schedule() (guiapi.WeekSchedule, error) {
	return guiapi.Schedule()
}

// RefreshSchedule re-fetches the calendar, ignoring the cache.
func (a *App) RefreshSchedule() (guiapi.WeekSchedule, error) {
	return guiapi.RefreshSchedule()
}

// SearchScheduleEntry searches every source for a calendar entry, going
// through the same multi-variant search a catalog click uses.
func (a *App) SearchScheduleEntry(e guiapi.ScheduleEntry, source string) ([]guiapi.SearchResult, error) {
	return guiapi.SearchTitles(guiapi.ScheduleItem(e), source)
}

// --- browser gate --------------------------------------------------------

// GetGateStatus reports whether a source's bot check can be cleared here.
func (a *App) GetGateStatus(source string) guiapi.GateStatus {
	return guiapi.GetGateStatus(source)
}

// PrepareSource runs a source's warm-up ahead of playback, so a screenless
// host or a missing helper browser surfaces before the user hits play.
func (a *App) PrepareSource(source string) (guiapi.GateStatus, error) {
	return guiapi.PrepareSource(source)
}

// GetGateOptions returns the helper-browser settings in effect.
func (a *App) GetGateOptions() guiapi.GateOptions {
	return guiapi.GetGateOptions()
}

// SetGateOptions applies the helper-browser settings. They take effect on
// the next playback; nothing needs restarting.
func (a *App) SetGateOptions(opts guiapi.GateOptions) error {
	return guiapi.SetGateOptions(opts)
}

// --- library: favorites and history --------------------------------------

// Favorites returns the bookmarked titles, most recently added first.
func (a *App) Favorites() []guiapi.FavoriteItem {
	return guiapi.Favorites()
}

// ToggleFavorite bookmarks or unbookmarks a title, returning the new state.
func (a *App) ToggleFavorite(r guiapi.SearchResult) (bool, error) {
	return guiapi.ToggleFavorite(r)
}

// IsFavorite reports whether a single title is bookmarked. The frontend
// uses it for the header star, where the backend's own key derivation is
// more reliable than reconstructing the key in JS.
func (a *App) IsFavorite(r guiapi.SearchResult) bool {
	return guiapi.IsFavorite(r)
}

// FavoriteKeys returns every bookmarked title's key, so a result grid can
// be marked without one call per card.
func (a *App) FavoriteKeys() []string {
	return guiapi.FavoriteKeys()
}

// TitleKey returns the identifier used to match a result against
// FavoriteKeys and history entries.
func (a *App) TitleKey(r guiapi.SearchResult) string {
	return guiapi.TitleKey(r)
}

// RecentlyWatched returns up to n distinct titles from the watch history.
func (a *App) RecentlyWatched(n int) []guiapi.HistoryEntry {
	return guiapi.RecentlyWatched(n)
}

// History returns every watch entry, most recent first.
func (a *App) History() []guiapi.HistoryEntry {
	return guiapi.History()
}

// WatchedEpisodes returns the episode keys already watched for a title.
func (a *App) WatchedEpisodes(r guiapi.SearchResult) []string {
	return guiapi.WatchedEpisodes(r)
}

// ForgetTitle drops every history entry for one title.
func (a *App) ForgetTitle(key string) error {
	return guiapi.ForgetTitle(key)
}

// ClearHistory empties the watch history, leaving favorites untouched.
func (a *App) ClearHistory() error {
	return guiapi.ClearHistory()
}

// LibraryPath returns where favorites and history are stored on disk.
func (a *App) LibraryPath() string {
	return guiapi.LibraryPath()
}

// --- playback ------------------------------------------------------------

// Qualities returns the quality preferences offered by the picker.
func (a *App) Qualities() []guiapi.QualityOption {
	return guiapi.Qualities()
}

// ResolveStreamURL returns a playable stream URL without launching a
// player, which is useful to copy or debug a stream.
func (a *App) ResolveStreamURL(r guiapi.SearchResult, ep guiapi.EpisodeResult) (string, error) {
	return guiapi.ResolveStreamURL(r, ep)
}

// PlayEpisode resolves an episode at the requested quality and launches it
// with the given player. An empty playerPath (or "mpv") uses mpv through
// the same IPC setup the CLI uses.
func (a *App) PlayEpisode(r guiapi.SearchResult, ep guiapi.EpisodeResult, playerPath, quality string) error {
	return guiapi.PlayEpisode(r, ep, playerPath, quality)
}

// PlayerAvailable reports whether a player alias resolves to something
// runnable, so the UI can grey out players that are not installed.
func (a *App) PlayerAvailable(name string) bool {
	return guiapi.PlayerAvailable(name)
}

// SourceNeedsBrowser reports whether playback from this source may open a
// real browser window to clear a bot check.
func (a *App) SourceNeedsBrowser(source string) bool {
	return guiapi.SourceNeedsBrowser(source)
}

// --- downloads -----------------------------------------------------------

// StartDownload queues an episode download and returns its job ID. Progress
// arrives as "download:progress" events.
func (a *App) StartDownload(r guiapi.SearchResult, ep guiapi.EpisodeResult, quality string) (string, error) {
	return guiapi.StartDownload(r, ep, quality)
}

// DownloadStatus returns a snapshot of every known job, newest first.
func (a *App) DownloadStatus() []guiapi.DownloadProgress {
	return guiapi.DownloadStatus()
}

// CancelDownload stops a running job and reports whether it was running.
func (a *App) CancelDownload(id string) bool {
	return guiapi.CancelDownload(id)
}

// ClearFinishedDownloads drops finished jobs from the panel.
func (a *App) ClearFinishedDownloads() {
	guiapi.ClearFinishedDownloads()
}

// DownloadsFolder returns the directory downloads are written to.
func (a *App) DownloadsFolder() string {
	return guiapi.DownloadsFolder()
}

// OpenDownloadsFolder reveals the downloads directory in the system file
// manager.
func (a *App) OpenDownloadsFolder() error {
	dir := guiapi.DownloadsFolder()

	// Check before launching. The path is not a constant -- it is the -o
	// flag's value when one was given -- and handing a stale or misspelled
	// one to the file manager is a silent no-op on Windows, where explorer
	// reports success regardless. Failing here says which directory was
	// missing instead.
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("a pasta de downloads não existe: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q não é uma pasta", dir)
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// #nosec G204 -- the program is a literal; only the argument varies,
		// and it is a directory this process just confirmed exists. It comes
		// from the -o flag the user running the app chose for their own
		// downloads, so there is no privilege boundary being crossed.
		cmd = exec.Command("explorer", dir)
	case "darwin":
		// #nosec G204 -- see the Windows branch above.
		cmd = exec.Command("open", dir)
	default:
		// #nosec G204 -- see the Windows branch above.
		cmd = exec.Command("xdg-open", dir)
	}
	// explorer.exe returns a non-zero exit code even when it succeeds, so
	// the error is deliberately not propagated on Windows.
	startErr := cmd.Start()
	if runtime.GOOS == "windows" {
		return nil
	}
	return startErr
}

// --- misc ----------------------------------------------------------------

// CopyToClipboard puts text on the system clipboard via the Wails runtime.
func (a *App) CopyToClipboard(text string) error {
	return wruntime.ClipboardSetText(a.ctx, text)
}

// PickPlayer opens a native file picker so the user can choose an
// executable to use as the video player. Returns the absolute path, or an
// empty string if the dialog was cancelled.
func (a *App) PickPlayer() (string, error) {
	return wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "Escolha o programa de vídeo",
		Filters: []wruntime.FileFilter{
			{DisplayName: "Programas (*.exe)", Pattern: "*.exe"},
			{DisplayName: "Todos os arquivos (*.*)", Pattern: "*.*"},
		},
	})
}
