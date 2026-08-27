# GoAnime Desktop (Wails GUI)

GoAnime ships an experimental Wails v2 desktop frontend alongside the
terminal UI. Both share **the same Go code**: the GUI calls into
`internal/guiapi`, which is a thin headless layer over the Model B source
registry (`internal/api/source` + `internal/api/providers`) — the very
dispatch path the CLI uses. No scraping or mpv logic is duplicated.

Verified against the tree at **v1.8.6**.

## Layout

```
cmd/
  goanime/         # CLI/TUI entrypoint (unchanged)
  goanime-gui/     # Wails entrypoint
    main.go        # wails.Run + embed of frontend/dist
    app.go         # App struct — bound to JS as window.go.main.App.*
    frontend/
      dist/        # static HTML/CSS/JS served by the webview
        index.html
        style.css
        js/        # native ES modules — no bundler, no Node toolchain
internal/
  guiapi/
    guiapi.go      # search, episodes, seasons, stream resolution
    players.go     # external player launching + referer handling
    metadata.go    # AniList covers and episode thumbnails
    browse.go      # seasonal catalog (AniList)
    schedule.go    # weekly airing calendar (AniList)
    library.go     # favorites and watch history
```

The frontend is deliberately static (no vite, no Node toolchain) so
`go build ./cmd/goanime-gui` works out of the box.

### Why ES modules and not a framework

The frontend is **native ES modules**, loaded with a single
`<script type="module" src="js/main.js">`. WebView2 is Chromium, so they run
untranspiled, and Wails serves `.js` as `text/javascript`. That keeps `go build`
the only command needed — a bundler would cost that, and the payoff would be
small: the bugs this GUI has actually had were about lifecycle and state, which
no rendering framework prevents.

The import graph is a **DAG in six layers**, and that is a property worth
keeping:

```
bridge  dom  state  labels  hooks     ← nothing imports upward
        views  sources  downloads
             cards  playback
        episodes  library  search
         catalog  gate  schedule
                 main                 ← the only module that knows them all
```

Two edges would have closed cycles, and both are inverted rather than allowed:

- **Tab loaders.** `views.js` would import `schedule.js` for its loader, while
  `schedule.js` imports `showResults` back. Instead `views.js` exposes
  `registerTab(id, pane, load)` and `main.js` registers the four tabs.
- **`hooks.js`.** A card opens a title (episodes) and toggling its star
  refreshes the library and the calendar — all of which are built from cards.
  `cards.js` calls `hooks.openTitle` / `hooks.libraryChanged`; `main.js` fills
  the two slots at boot. Named slots rather than an event bus, because these are
  the only two edges that need it and an unfilled slot fails at the call site
  instead of silently going nowhere.

Modules have their own scope, so `state` and `els` are no longer global. The
one door left open is `window.__debug = { state, els }`, set at the end of
`main.js`, for devtools and for the test harness that drives the UI over CDP.

## Running the GUI

### Go build

Wails v2 **requires build tags**. Without them the binary compiles but
refuses to start, popping up:

> Wails applications will not build without the correct build tags.

```powershell
go build -tags "desktop,production" -ldflags "-s -w -H windowsgui" -trimpath -o goanime-gui.exe ./cmd/goanime-gui
.\goanime-gui.exe
```

- `desktop,production` are the tags Wails checks for; omit them and you get
  the dialog above instead of a window.
- `-H windowsgui` suppresses the console window that would otherwise open
  behind the app on Windows. Drop it while debugging so `fmt.Println` and
  panics stay visible.

On Linux and macOS the same tags apply; only `-H windowsgui` is
Windows-specific.

This produces a standalone executable that embeds the frontend and opens a
native WebView2 window on Windows.

> **mpv is still required** for the default player — the GUI reuses
> `player.StartVideo`, so mpv must be on PATH just as for the CLI. Other
> players (VLC, MPC-HC, or any executable you pick) are launched directly.

### Application icon

The icon lives in several places, because each platform — and, on Windows,
each *consumer* — gets it by a different mechanism:

| File | Used by |
|---|---|
| `cmd/goanime-gui/appicon.png` | `go:embed` → the Wails window icon on Linux and the macOS About box |
| `cmd/goanime-gui/winres/winres.json` + `winres/appicon.png` | Source for the Windows resource |
| `cmd/goanime-gui/rsrc_windows_amd64.syso` | Linked automatically by `go build` → Explorer, taskbar and the window |
| `cmd/goanime-gui/icon_windows.go` | Sets the large window icon at runtime (see below) |
| `build/appicon.png` | `wails build`, which looks for it by convention |

On Windows the icon is **not** the embedded PNG: it comes from the
executable's resource table. `go build` links any `.syso` in the main
package automatically, so a plain build produces an icon'd binary with no
extra step.

To change the icon, replace the PNGs and regenerate:

```powershell
go generate ./cmd/goanime-gui
```

That runs [go-winres](https://github.com/tc-hib/go-winres) over
`winres/winres.json`, producing a multi-size ICO (256, 64, 48, 32, 16 — all
PNG-compressed, 32-bit alpha) plus the version block. The `.syso` is
committed so a fresh clone needs no tooling.

#### Three traps, all of which fail silently

1. **The icon must be emitted under resource ID 3 as well as 1.** The
   Windows shell reads the *lowest* icon group for Explorer and the taskbar,
   but Wails loads the window icon by the fixed ID 3
   (`winc.AppIconID`). With only ID 1 present — which is what go-winres'
   `simply` shortcut produces — the file gets an icon and the running window
   does not. This is why the resource is built from `winres.json`.

2. **`file_version` is not optional.** Without it the version block is
   written but Windows reads every string back as empty: `ProductName`,
   `FileDescription` and the rest all come out blank in Explorer's Details
   tab, with no error anywhere.

3. **Wails never sets `ICON_BIG`.** winc's `Form` calls
   `SetIcon(ICON_SMALL, …)` only, so Alt+Tab and the large taskbar preview
   fall through to the window-class icon, which is never populated either —
   giving the generic executable icon. `icon_windows.go` fills that in: it
   finds the process's own top-level window and sends `WM_SETICON` for both
   sizes. It polls for the window (`OnStartup` can fire before it is on
   screen) and gives up quietly, because a wrong Alt+Tab icon is a blemish,
   never a reason to fail startup.

### Window styling

The title bar and its minimise / maximise / close buttons follow Windows'
own light/dark setting (`Theme: windows.SystemDefault`), tinted to the app's
palette through `CustomTheme` so the caption is not a stock grey strip above
a dark page. The frontend follows the same system setting via
`prefers-color-scheme`, so both halves switch together.

Windows 11 supplies the rounded corners and the modern caption buttons for
any standard window; what actually needed setting is the theme, which is
what `DWMWA_USE_IMMERSIVE_DARK_MODE` keys off.

**Mica is deliberately off.** Wails only applies a backdrop when
`WindowIsTranslucent` is set, which makes the *whole* window see-through —
cover art and text would wash out against the desktop, and the material
would be hidden behind the opaque page anyway. `BackdropType` is set to
`None` explicitly so the choice is visible rather than implied.

### Using the Wails CLI (optional, for hot reload)

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails doctor
wails dev
wails build
```

`wails.json` at the repo root already points at `cmd/goanime-gui/frontend`.

## How it works

1. On boot the frontend calls `App.Sources`, which enumerates
   `source.ActiveSources()`. The dropdown is therefore generated from the
   registry — a source removed upstream or switched off through
   `GOANIME_DISABLED_SOURCES` simply stops appearing, and a new one shows
   up with no frontend edit.
2. `App.Search` → `guiapi.Search` → `providers.SearchAll(ctx, query, kinds…)`,
   the same concurrent fan-out (circuit breaker, kill-switch, language
   tagging included) the CLI runs, minus the fuzzy finder.
3. `App.GetEpisodes` → `providers.FetchEpisodes`. **SuperFlix is special:**
   `api.GetSuperFlixEpisodes` runs an interactive season picker that needs a
   TTY, so `guiapi` lists the seasons itself and returns them to the
   frontend, which renders a dropdown instead. See below — that path has
   sharp edges.
4. `App.PlayEpisode` resolves through
   `player.GetVideoURLForEpisodeEnhanced(ctx, …)` and then either hands the
   URL to mpv via the CLI's IPC launcher or spawns the chosen external
   player with a `--http-referrer` style flag.

Because the GUI dispatches through the registry, **any scraper fix or new
source added under `internal/scraper/providers` works in the GUI with no
changes here**.

## Usability features

| Feature | Where |
|---|---|
| Source dropdown built from the live registry | `App.Sources` |
| Search cancellation (button or <kbd>Esc</kbd>) | `App.CancelSearch` |
| Search history (last 12 queries, `localStorage`) | frontend only |
| Loading skeletons for results and episodes | frontend only |
| Inline error state with a **Try again** button | frontend only |
| Player chosen once and remembered; changed from the header chip | `App.PlayerAvailable`, `localStorage` |
| Players that are not installed are marked in the picker | `App.PlayerAvailable` |
| Season switcher for seasoned sources | `App.GetSeasonEpisodes` |
| Episode filter box (shown past 12 episodes) | frontend only |
| **Copy link** on each episode card | `App.ResolveStreamURL` + `App.CopyToClipboard` |
| Warning badge and note for browser-gated sources | `App.SourceNeedsBrowser` |
| Keyboard: <kbd>Ctrl</kbd>+<kbd>K</kbd> / <kbd>/</kbd> to search, <kbd>Esc</kbd> to go back | frontend only |
| Follows the OS light/dark theme | `style.css` |
| Filter results by name, sort by name or year | frontend only |
| Quality picker on every episode, remembered between sessions | `App.Qualities` |
| Play **or** download from the same episode dialog | `App.PlayEpisode`, `App.StartDownload` |
| Downloads drawer: live progress, cancel, clear, open folder | `App.DownloadStatus`, `download:progress` events |
| Favorites: star a title, home screen keeps them | `App.ToggleFavorite`, `App.Favorites` |
| Continue watching: recent titles, watched episodes ticked | `App.RecentlyWatched`, `App.WatchedEpisodes` |
| Episode artwork falls back to the AniList poster | `App.GetEpisodeArt` |

## The human check (browser-gated sources)

SuperFlix sits behind a Cloudflare Turnstile gate. The solver for it is
**upstream code**, not something the GUI adds: `internal/scraper/providers/superflix`
drives a real Chrome through Playwright, with a persistent profile so the
`cf_clearance` cookie survives between runs and the check is normally only
seen once.

What the GUI adds is making that predictable instead of surprising:

| Piece | What it does |
|---|---|
| `App.GetGateStatus(source)` | Reports whether the check can be cleared here — no screen, first-run setup pending, or ready — in plain language |
| `App.PrepareSource(source)` | Runs `source.WarmUp` **before** playback, so a screenless host surfaces at a moment the user chose |
| `App.GetGateOptions` / `SetGateOptions` | Surfaces the solver's existing env knobs (`GOANIME_SF_HEADLESS`, `GOANIME_SF_BUNDLED`, `GOANIME_SF_CHROME_CHANNEL`) as GUI settings |

`PrepareSource` deliberately does **not** force an eager solve. The solver
clears the gate as part of the first real request and caches the result in
its browser profile; a separate eager solve would just open a second window
for no benefit.

Note that "hide the browser window" (headless) usually makes the check
*fail* — Turnstile wants a real on-screen window. It is an escape hatch for
screenless hosts, which is why the settings panel says so rather than
presenting it as a neutral toggle.

## Release dates

`App.GetTitleInfo` returns the release date, format, status, episode count
and score for a title. AniList often knows only part of a date, so
`formatRelease` renders every level of precision sensibly — `6 Apr 2019`,
`Apr 2019`, `Spring 2019` or just `1998` — instead of printing zeroes for
the missing parts. When AniList has no entry at all, the scraper's own year
survives rather than the field going blank.

Episode cards show the per-episode air date whenever the source reports one
(SuperFlix always does; it was already in `EpisodeResult.Aired` and simply
went unrendered).

All of this comes from **one** cached AniList query per title — the same one
that backs covers and episode stills — so a grid of 40 results does not
multiply into hundreds of API calls. `GetCover` checks that cache before
falling back to the shared `api.FetchAnimeFromAniList` client.

## Tabs

The main view is four tabs — **Calendário**, **Catálogo**, **Favoritos**,
**Histórico** — rather than one long scrolling page.

**Search results are not a tab.** The header's search box is the only way to
them, so a tab would have been a second door to the same room, empty until
someone used the first one. They open *over* the tabs instead, like the
episode list, with their own way back.

The reason is not only navigation. **Only the active tab loads.** AniList is
rate-limited, and a cold start that fetched the calendar (several pages), the
catalog, the genre list and every favorite's artwork at once reliably earned
a 429 — the user saw *"o AniList pediu para esperar um pouco"* where the
calendar should be. Opening on the calendar alone cuts that to a handful of
requests.

- A tab's loader runs the **first time it is opened**, and never again on its
  own — but **a loader that fails clears the mark, so opening the tab again
  retries**. Without that, one transient AniList hiccup left the tab blank for
  the rest of the session with no way back: clicking it again did nothing,
  because it was already marked loaded. Data that can go stale is refreshed by the action that changed it:
  starring a title reloads Favoritos and the calendar's highlights, clearing
  the history reloads Histórico.
- The Favoritos and Histórico badges are filled at boot from a **local** read
  of the library file, so the counts are there without loading either tab.
- The episode list is **not** a tab either. It opens on top of whatever was
  on screen and remembers what that was, so "← Voltar" returns to the search
  results if that is where you came from, and to the tab otherwise.
- Every bridge call that paints a skeleton is wrapped in **`withTimeout`**. A
  call that never settles used to leave that skeleton up with no error and no
  way out — indistinguishable from loading forever.

## Weekly calendar

The Calendário tab shows the **real week, Monday to Sunday** — the one
containing today, not seven days rolling forward from now. It is the default
tab because "what comes out today?" is the question a viewer opens the app
with.

The days already gone are the point of running Monday-first: *"what came out
on Monday"* is exactly what someone opening the app on Thursday wants, and a
rolling window hides it. Past days are dimmed at the header, never dropped,
and their episodes stay fully legible — whole columns are past by midweek.

Each column is one day; each row is one airing, showing the cover, the title,
the episode number and the local broadcast time. Today's column is outlined,
and the three days around it are labelled *Ontem* / *Hoje* / *Amanhã* with
their weekday alongside, since those names alone do not say which day they
are.

**Favorites are the point of it.** A title in the user's library gets an
accent stripe, a tinted row and a star, the column header carries a `★ n`
badge, and a "Somente favoritos" checkbox (remembered across sessions)
collapses the calendar down to them. With no favorites airing that week the
filter says so instead of blanking the grid.

| Call | Purpose |
|---|---|
| `App.Schedule()` | The seven days ahead, favorites already marked |
| `App.RefreshSchedule()` | The same, ignoring the cache |
| `App.SearchScheduleEntry(entry, source)` | Search every source for one airing |

### Matching an airing to a favorite

There is **no shared identifier** to join on: favorites are scraper results
keyed by source URL, the calendar is AniList metadata with no scraper
identity at all. So the match is a name comparison, and the normalisation
does the work — lowercase, accents folded, punctuation and spaces dropped,
and the words the PT-BR sources decorate titles with ("Dublado", "Todos os
Episódios") removed. `SPY×FAMILY` and `Spy x Family Dublado` land on the
same key. AniList's `synonyms` are folded in on the way, so a localised name
matches too.

The comparison is then **exact**, deliberately. A favorite saved as
"Kimetsu no Yaiba 3rd Season" does not match AniList's "Kimetsu no Yaiba:
Katanakaji no Sato-hen" — a near-miss like that is better left unmarked than
wrongly starred, and containment matching would star *One Piece Film: Red*
for anyone who follows *One Piece*.

### Details that are easy to get wrong

- **Local midnight.** `time.Time.Truncate` rounds in UTC, so it lands
  mid-day for most of the world. The day boundaries are built with
  `time.Date` in the user's own location instead.
- **Monday-first arithmetic.** `time.Weekday` counts Sunday as 0, so the
  naive offset puts Sunday at the *start* of the week that has just begun.
  `weekStartFor` shifts by 6 first — that is the case a test pins down.
- **Relative day names are compared as dates**, not by subtracting and
  dividing by 24h, which goes wrong across a DST boundary.
- **Both AniList bounds are exclusive.** `airingAt_greater` / `_lesser`
  would drop an episode airing exactly at midnight, so the window is
  widened by a second on each side.
- **`airingSchedules` takes no `isAdult` argument** the way `media` does, so
  adult titles are filtered out after the fetch rather than in the query.
- **Seven columns, always**, even on a day nothing airs. A grid that grew
  and shrank would move under the cursor as the week progressed.
- The week is cached for 15 minutes and re-laid-out per call, so starring a
  title updates the calendar without touching the network.

## Staying under AniList's rate limit

AniList allows roughly 90 requests a minute and answers `429` past that. Every
call this package makes goes through one function, `anilistPost`, and that is
where the limit is handled:

- **One gate, held across the wait.** Requests are spaced 700 ms apart, and
  the gate is held for the whole sleep on purpose — that is what turns a burst
  of parallel callers into a queue instead of a stampede.
- **429 is retried, not surfaced.** The `Retry-After` header says how long to
  wait; the wait is capped at 5 s, because sitting behind a spinner for the
  minute AniList sometimes asks for is worse than failing and letting the user
  press Atualizar.
- **Cover lookups go through it too.** `metadata.go` used to issue its own
  requests, which meant a home screen asking for a dozen covers bypassed the
  pacing entirely — exactly the burst that tripped the limit.

Combined with the per-tab loading above, a cold start is a handful of calls
rather than a dozen.

### Covers that do not load

A calendar week is ~120 cover images requested at once, and AniList's image
host drops some of them under that burst — the URLs are valid, the connections
are not. `setArtwork` retries a failed image **once** after a beat, and only a
second failure falls back to a tile with the title's first letter. The earlier
behaviour (hide the image) left a blank gap that read as missing data rather
than a slow load.

## Catalog

The Catálogo tab. It loads the first time you open it, not at boot.

Six listings, each combinable with a year, genre and format filter:

| Mode | AniList sort |
|---|---|
| Por temporada | `POPULARITY_DESC`, pinned to a season + year |
| Em alta agora | `TRENDING_DESC` |
| Em exibição | `TRENDING_DESC` + `status: RELEASING` |
| Mais populares | `POPULARITY_DESC` |
| Melhores notas | `SCORE_DESC` |
| Ainda vão lançar | `POPULARITY_DESC` + `status: NOT_YET_RELEASED` |

The data comes from **AniList**, not from the scrapers: none of the sources
expose a "what aired in Spring 2024" endpoint, and AniList is the canonical
anime database this package already queries.

That has a consequence worth being explicit about — **a catalog entry is
metadata, not a playable item.** Clicking one searches the sources, so the
user sees which ones actually carry it rather than being promised something
that may exist nowhere. When nothing is found, the empty state says so
plainly instead of looking like a failure.

| Call | Purpose |
|---|---|
| `App.Browse(query)` | One page; the zero query means the season airing now |
| `App.SearchTitles(item, source)` | Search every title variant for a catalog entry |
| `App.ModeOptions` / `SeasonOptions` / `YearOptions` / `GenreOptions` / `FormatOptions` | Picker contents, in Portuguese |
| `App.CurrentSeasonYear` / `CurrentSeasonName` | The season airing now |

### Details that are easy to get wrong

- **December.** AniList counts a December premiere as the *next* year's
  winter, so `seasonFor` rolls the year over. Returning `2024/WINTER` in
  December 2024 would show a season that ended eleven months ago.
- **No total count.** AniList caps `pageInfo.total` at 5000 for *every*
  query — "1998 action anime" reports the same 5000 as "all anime". The
  field is deliberately absent from `BrowsePage`; `HasNextPage` is accurate
  and is all the pager needs.
- **Season is dropped outside season mode.** A stale value left in the
  dropdown would otherwise narrow "Melhores notas" to one season silently.
- **Pages are cached** per full query. AniList is rate-limited, so the pager
  and switching filters back and forth cost one request each, once.
- **Genres are fetched from AniList** (`GenreCollection`) so the filter
  stays in sync, translated through a map that falls back to AniList's own
  spelling — a genre added upstream still works, just untranslated. Hentai
  is excluded because `isAdult: false` filters it out of every query anyway,
  so offering it would only produce empty pages.

### Why a catalog click used to return English-only results

Two causes, both fixed:

1. **The click reused the header's source dropdown**, which persists in
   `localStorage`. A user who had once picked AllAnime got English-only
   results from every catalog click, with nothing on screen explaining why.
   Catalog clicks now always search **all** sources.
2. **Only the romaji title was searched.** `SearchTitles` now runs the
   romaji, English and display names concurrently and merges the hits,
   de-duplicated by the same key the library uses. Measured on real
   searches, this raised Demon Slayer from 36 to 39 results and Attack on
   Titan from 58 to 62.

The variants share **one** search context (`beginSearch`), because
`Search` cancels the previous in-flight search — three concurrent calls to
it would have cancelled each other.

Result cards also carry a **language badge** now (PT-BR in green), so it is
visible at a glance which results are Portuguese rather than being implied
by the source name.

## Language

The GUI ships in **Brazilian Portuguese**: every button, label, status
line, toast and error message the user can see. That includes the strings
that originate in Go — `guiapi`'s error text, the quality labels, the
download states and the human-check guidance — because those surface
verbatim in the status bar and the downloads panel.

What stays in English, on purpose:

- **Code comments, identifiers and log/debug output**, matching the rest of
  the repository.
- **Loanwords Brazilian Portuguese actually uses**: "download", "player".
  Translating those would read worse, not better.
- **Product and source names**: mpv, VLC, AniList, SuperFlix, AllAnime.

`internal/guiapi/i18n_test.go` keeps this honest. It scans the shipped
`index.html` and `main.js` for English words in user-visible positions
(element text, `placeholder`/`title`/`aria-label`, and the arguments of
`setStatus`/`toast`/`textContent`), and checks the Go-side labels, error
messages, month abbreviations and season names the same way. Add an English
string to the UI and the test names the file and the string.

It matches whole words only, skips `${…}` interpolations (those are variable
names, not text) and carries an allow-list for the loanwords above — so it
fails on real regressions rather than on correct Portuguese.

## Listing SuperFlix seasons

`guiapi.fetchSuperFlixSeasons` mirrors the ordering and the budgets of
`api.fetchSuperFlixSeasons`, which is unexported. Both matter, and getting
either wrong fails in a way that looks like a broken source:

1. **TVmaze first, over plain HTTP.** It is keyed by IMDB ID, which the
   scrapers do not supply, so `movie.EnrichMedia` runs first to obtain one.
   That works with no API key — TMDB is skipped when `TMDB_API_KEY` is
   unset and OMDb answers on its public demo key. Measured end to end:
   enrichment ~300 ms, TVmaze ~900 ms.

2. **The headed browser only as a fallback**, with a **210 s** budget.

That budget is not padding. The SuperFlix client allows itself a Cloudflare
solve budget of its own; a context shorter than that cancels the solve
mid-flight, and the failure surfaces as:

```
não foi possível carregar as temporadas do SuperFlix:
failed to load serie page: context deadline exceeded
```

The GUI originally gave this path the shared 60 s episode budget and skipped
TVmaze entirely, so it went to the browser every time and frequently ran
out. SuperFlix now builds its own context rather than inheriting the shared
one, and `TestSuperFlixBrowserTimeoutOutlastsTheSolve` pins the relationship
between the three timeouts so it cannot silently regress.

`describeSuperFlixFailure` translates the remaining failures into something
actionable — a timeout says the bot check probably did not finish and
suggests another source, rather than printing `context deadline exceeded` at
the user. Unrecognised causes are passed through verbatim so debugging is
still possible.

Measured after the fix: Breaking Bad went from a 60 s timeout to **1.2 s**,
five seasons, with episode titles and air dates, and no browser window.

## Episode artwork

Episode thumbnails come from AniList's `streamingEpisodes`, which only
covers well-known series. Everything else falls back to the title's poster
— and that is where the grid used to render blank: most scrapers return an
empty `ImageURL`, and while the *result cards* recovered by asking AniList
for a cover, the *episode grid* had no such second source.

`App.GetEpisodeArt` fixes that by returning both in one bridge call:

```go
type EpisodeArt struct {
    Poster string            // scraper image, or the AniList cover
    Thumbs map[string]string // episode number -> still
}
```

The poster lookup reuses `GetCover`'s cache, so opening a title the results
grid already rendered costs nothing extra.

## Favorites and history

Stored as plain JSON at `~/.local/goanime/gui-library.json`, alongside the
downloads directory the CLI already uses.

It is deliberately **not** the SQLite tracker in `internal/tracking`: that
one is behind the `cgo` build tag, so a standard CGO-disabled build has no
tracking at all. The GUI's library works in every build, and the user can
read or delete the file by hand.

- Writes are atomic (temp file, then rename), so a crash mid-write leaves
  the previous version intact rather than a truncated file.
- A missing or corrupt file reads as an empty library instead of failing.
- A title is keyed by `source|url`, falling back to `source|name:<normalised>`
  for the few results that carry no URL.
- History is capped at 300 entries, deduplicated per episode (re-watching
  moves an entry up rather than duplicating it) and season-aware, so two
  seasons both numbered from 1 do not collide.
- `RecordWatch` runs from `PlayEpisode` **only after the player actually
  starts**, so a failed launch does not pollute the history.

## Downloads

The CLI's download stack (`internal/downloader`) is built around Bubble Tea
progress models and `huh` prompts, so the GUI does not reuse it. Instead
[internal/guiapi/download.go](../internal/guiapi/download.go) runs a headless
job queue on top of the same primitives the CLI ultimately calls:

- `hls.DownloadToFile` for m3u8 streams (progress by segment count).
- A plain streaming GET for direct files (progress by bytes), guarded by
  `api.ValidateExternalURL` — the same SSRF check the rest of the tree uses.
- `util.PlexEpisodeFilename` / `util.FormatPlexEpisodeDir` for the output
  path, so GUI and CLI downloads land in **one library**, not two.

Progress reaches the frontend as `download:progress` Wails events. `guiapi`
itself has no Wails import: it exposes `SetProgressHook`, and `app.go` wires
that to `runtime.EventsEmit` at startup.

Quality is a *preference*, not an enumeration — the sources do not publish a
uniform ladder, so the picker mirrors the CLI's `--quality` vocabulary and
each scraper resolves it to the nearest stream it has.
`util.GlobalQuality` is process-wide state, so `resolveWithQuality` sets and
restores it under a mutex around each resolution; a download can therefore
run at a different quality than a simultaneous playback without either one
corrupting the other.

## What is intentionally not in the GUI yet

- Batch / range downloads (still CLI-only: `goanime -d -r "name" 1-12`).
- Discord Rich Presence, AniSkip intro skipping, resume-playback.
- Anime4K upscaling.

These are deliberate omissions. The architecture
(`App` methods → `guiapi` → `internal/api/providers`) is where to add them.
