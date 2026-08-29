package guiapi

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The weekly schedule answers a question the catalog cannot: "what comes out
// today?". AniList's AiringSchedule is the only source here that knows the
// broadcast timestamp of an individual episode, so the calendar is built from
// it rather than from the scrapers — and, like the catalog, an entry is
// metadata: clicking one runs a normal search for the title.

// scheduleDays is the length of the calendar. It is the real week — Monday
// through Sunday, the one containing today — rather than seven days rolling
// forward from now.
//
// The days already gone are the point of the difference: "what came out on
// Monday" is exactly what someone opening the app on Thursday wants to know,
// and a rolling window hides it. Past days are dimmed rather than dropped, so
// the eye still lands on what is still to come.
const scheduleDays = 7

// schedulePerPage is AniList's maximum page size, and schedulePageLimit caps
// the walk. A busy week has roughly 250 airings worldwide, so eight pages is
// headroom rather than a real ceiling — but it stops a broken cursor from
// looping forever.
const (
	schedulePerPage   = 50
	schedulePageLimit = 8
)

// scheduleTTL is how long a fetched week is reused. Airing times barely move
// within a session, and AniList is rate-limited; the refresh button exists for
// the rare case where they do.
const scheduleTTL = 15 * time.Minute

// ScheduleEntry is one episode airing on a given day.
type ScheduleEntry struct {
	AniListID int    `json:"anilistID"`
	Title     string `json:"title"`
	Romaji    string `json:"romaji"`
	English   string `json:"english"`
	Cover     string `json:"cover"`
	// Episode is the episode number airing, as AniList numbers it.
	Episode int `json:"episode"`
	// AiringAt is the broadcast instant in Unix seconds, and Time is the same
	// moment as "HH:MM" in the user's own timezone — the frontend never has to
	// guess which one it is holding.
	AiringAt int64  `json:"airingAt"`
	Time     string `json:"time"`
	Format   string `json:"format"`
	Status   string `json:"status"`
	// Favorite is true when this title is in the user's library. It is
	// recomputed on every call rather than cached, so starring a title updates
	// the calendar without a refetch.
	Favorite bool `json:"favorite"`
	// Aired marks an episode whose broadcast time has already passed, so the
	// UI can dim it instead of implying it is still coming.
	Aired bool `json:"aired"`
	// adult carries AniList's isAdult flag. The week is fetched and cached
	// whole, so the flag rides along on the entry and layOutWeek drops the
	// flagged ones on the way out. It never reaches the frontend.
	adult bool

	// matchKeys are the normalised title variants used to recognise a
	// favorite. They exist only for that comparison, so they stay unexported
	// and never reach the frontend.
	matchKeys []string
}

// ScheduleDay is one column of the calendar.
type ScheduleDay struct {
	// Date is ISO "2026-08-26", used as a stable key by the frontend.
	Date string `json:"date"`
	// Weekday is the short pt-BR weekday ("Qua"), Label is what the column
	// header shows ("Hoje", "Amanhã", or the weekday) and DateLabel is the day
	// and month ("26 ago").
	Weekday   string `json:"weekday"`
	Label     string `json:"label"`
	DateLabel string `json:"dateLabel"`
	Today     bool   `json:"today"`
	// Past marks a day earlier in the week than today, so the UI can dim the
	// whole column without comparing dates itself.
	Past bool `json:"past"`
	// Relative is true when Label reads "Ontem"/"Hoje"/"Amanhã" instead of a
	// weekday name — those say nothing about which weekday they fall on, so
	// the UI prints the weekday next to them.
	Relative bool `json:"relative"`
	// FavoriteCount is how many of Entries are favorites, so a column can be
	// badged without the frontend counting.
	FavoriteCount int             `json:"favoriteCount"`
	Entries       []ScheduleEntry `json:"entries"`
}

// WeekSchedule is the whole calendar: always exactly scheduleDays days, in
// order, including days with nothing on them. A missing day would make the
// grid jump around as the week progresses.
type WeekSchedule struct {
	Days []ScheduleDay `json:"days"`
	// Total and FavoriteTotal count the whole week.
	Total         int `json:"total"`
	FavoriteTotal int `json:"favoriteTotal"`
	// FetchedAt is when AniList was last asked, so the UI can say how fresh
	// the calendar is.
	FetchedAt time.Time `json:"fetchedAt"`
	// Partial is true when the page walk was cut short, meaning the tail of a
	// very busy week is missing.
	Partial bool `json:"partial"`
	// Stale marks a week served from the cache past its TTL. The data is
	// good enough to show — airing times barely move — but the frontend
	// should refresh behind it rather than leave it as the final answer.
	Stale bool `json:"stale"`
}

// scheduleSnapshot is one fetched week. Entries are stored without their
// Favorite flag; it is applied per call.
type scheduleSnapshot struct {
	// start is the Monday the week begins at, so a snapshot taken last week
	// is discarded rather than shown as if it were this one. Within a week it
	// is scheduleTTL that keeps the data fresh, and the "already aired" marks
	// are recomputed on every call regardless.
	start     time.Time
	fetchedAt time.Time
	partial   bool
	entries   []ScheduleEntry
}

var (
	scheduleMu    sync.Mutex
	scheduleCache *scheduleSnapshot
)

// Schedule returns the week ahead, with the user's favorites marked. Titles
// AniList flags as adult are left out of the calendar.
func Schedule() (WeekSchedule, error) {
	return buildSchedule(false)
}

// RefreshSchedule discards the cached week and fetches it again.
func RefreshSchedule() (WeekSchedule, error) {
	return buildSchedule(true)
}

func buildSchedule(force bool) (WeekSchedule, error) {
	start := weekStartFor(time.Now())
	end := start.AddDate(0, 0, scheduleDays)

	// A stored week is served whatever its age, and only its freshness is
	// reported. Blocking on the fetch is what made every launch cost a
	// paged walk through AniList — eight requests spaced by the rate
	// limiter — on the tab that opens first, with nothing on screen until
	// it finished. Stale data now paints instantly and the frontend
	// refreshes behind it.
	if !force {
		if snap := cachedWeek(start); snap != nil {
			week := layOutWeek(snap, start)
			week.Stale = time.Since(snap.fetchedAt) > scheduleTTL
			return week, nil
		}
	}

	entries, partial, err := fetchSchedule(start, end)
	if err != nil {
		return WeekSchedule{}, err
	}
	snap := &scheduleSnapshot{
		start:     start,
		fetchedAt: time.Now(),
		partial:   partial,
		entries:   entries,
	}

	scheduleMu.Lock()
	scheduleCache = snap
	scheduleMu.Unlock()
	storeWeek(snap)

	return layOutWeek(snap, start), nil
}

// cachedWeek returns the snapshot for the given week, from memory or from
// the file, or nil when neither holds this week. A snapshot from a week
// that has rolled over is no use and is not returned.
func cachedWeek(start time.Time) *scheduleSnapshot {
	scheduleMu.Lock()
	snap := scheduleCache
	scheduleMu.Unlock()
	if snap != nil && snap.start.Equal(start) {
		return snap
	}

	stored := metaCacheSchedule()
	if stored == nil || !stored.Start.Equal(start) {
		return nil
	}

	snap = &scheduleSnapshot{
		start:     stored.Start,
		fetchedAt: stored.FetchedAt,
		partial:   stored.Partial,
		entries:   make([]ScheduleEntry, 0, len(stored.Entries)),
	}
	for _, e := range stored.Entries {
		entry := e.Entry
		entry.adult = e.Adult
		entry.matchKeys = e.MatchKeys
		snap.entries = append(snap.entries, entry)
	}

	// Promote it to the in-memory cache so the next call this session does
	// not re-read and re-convert the file.
	scheduleMu.Lock()
	scheduleCache = snap
	scheduleMu.Unlock()
	return snap
}

// storeWeek writes a freshly fetched week to the file.
func storeWeek(snap *scheduleSnapshot) {
	stored := &cachedSchedule{
		Start:     snap.start,
		FetchedAt: snap.fetchedAt,
		Partial:   snap.partial,
		Entries:   make([]cachedScheduleEntry, 0, len(snap.entries)),
	}
	for _, e := range snap.entries {
		// Favorite and Aired are recomputed on every call, so they are
		// cleared rather than written down as they happened to be now.
		entry := e
		entry.Favorite = false
		entry.Aired = false
		stored.Entries = append(stored.Entries, cachedScheduleEntry{
			Entry:     entry,
			Adult:     e.adult,
			MatchKeys: e.matchKeys,
		})
	}
	metaCachePutSchedule(stored)
}

// layOutWeek splits the fetched entries into days and marks favorites,
// leaving out the titles AniList flags as adult.
func layOutWeek(snap *scheduleSnapshot, start time.Time) WeekSchedule {
	favs := favoriteMatchKeys()
	now := time.Now()
	today := startOfDay(now)

	week := WeekSchedule{
		Days:      make([]ScheduleDay, 0, scheduleDays),
		FetchedAt: snap.fetchedAt,
		Partial:   snap.partial,
	}

	// Index the days by date so a single pass over the entries fills them.
	byDate := make(map[string]int, scheduleDays)
	for i := 0; i < scheduleDays; i++ {
		day := start.AddDate(0, 0, i)
		date := day.Format("2006-01-02")
		byDate[date] = i

		label, relative := dayLabel(day, today)
		week.Days = append(week.Days, ScheduleDay{
			Date:      date,
			Weekday:   weekdayShort(day.Weekday()),
			Label:     label,
			DateLabel: fmt.Sprintf("%d %s", day.Day(), monthNames[int(day.Month())-1]),
			Today:     day.Equal(today),
			Past:      day.Before(today),
			Relative:  relative,
			Entries:   []ScheduleEntry{},
		})
	}

	for _, e := range snap.entries {
		if e.adult {
			continue
		}

		at := time.Unix(e.AiringAt, 0).Local()
		idx, ok := byDate[at.Format("2006-01-02")]
		if !ok {
			continue
		}
		e.Favorite = matchesFavorite(e, favs)
		e.Aired = at.Before(now)

		day := &week.Days[idx]
		day.Entries = append(day.Entries, e)
		if e.Favorite {
			day.FavoriteCount++
			week.FavoriteTotal++
		}
		week.Total++
	}

	return week
}

// startOfDay truncates to local midnight. time.Time.Truncate cannot be used:
// it works in UTC, so it lands mid-day in most timezones.
func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// weekStartFor returns the Monday of the week containing t, at local
// midnight. Monday-first is how a week is written and read in Brazil, and it
// is what makes "terminar no domingo" mean the end of the week rather than an
// arbitrary seventh day.
func weekStartFor(t time.Time) time.Time {
	d := startOfDay(t)
	// time.Weekday counts Sunday as 0, so shifting by 6 puts Monday at 0 and
	// Sunday at 6 — the offset back to this week's Monday.
	offset := (int(d.Weekday()) + 6) % 7
	return d.AddDate(0, 0, -offset)
}

// dayLabel names a column. The three days around today get relative names
// because that is how people talk about them; the rest get their weekday. The
// second result says which kind was used, since a relative name has to be
// shown with the weekday to stay unambiguous.
//
// Both arguments must be local midnights, so the comparison is between whole
// days — subtracting and dividing by 24h would go wrong on a DST boundary.
func dayLabel(day, today time.Time) (label string, relative bool) {
	switch {
	case day.Equal(today):
		return "Hoje", true
	case day.Equal(today.AddDate(0, 0, 1)):
		return "Amanhã", true
	case day.Equal(today.AddDate(0, 0, -1)):
		return "Ontem", true
	}
	return weekdayLong(day.Weekday()), false
}

var weekdaysLong = [...]string{
	"Domingo", "Segunda", "Terça", "Quarta", "Quinta", "Sexta", "Sábado",
}

var weekdaysShort = [...]string{
	"Dom", "Seg", "Ter", "Qua", "Qui", "Sex", "Sáb",
}

func weekdayLong(d time.Weekday) string  { return weekdaysLong[int(d)] }
func weekdayShort(d time.Weekday) string { return weekdaysShort[int(d)] }

// scheduleQuery asks for one page of airings in a time window. Both bounds are
// exclusive on AniList's side, which fetchSchedule compensates for.
const scheduleQuery = `query ($page: Int, $perPage: Int, $from: Int, $to: Int) {
	Page(page: $page, perPage: $perPage) {
		pageInfo { hasNextPage }
		airingSchedules(airingAt_greater: $from, airingAt_lesser: $to, sort: TIME) {
			episode
			airingAt
			media {
				id
				isAdult
				title { romaji english native }
				synonyms
				format
				status
				coverImage { large medium }
			}
		}
	}
}`

// fetchSchedule walks the airing schedule for the window, returning the
// entries in broadcast order. The second result is true when the week is
// incomplete — the walk hit schedulePageLimit, or a later page failed.
func fetchSchedule(start, end time.Time) ([]ScheduleEntry, bool, error) {
	var (
		out     []ScheduleEntry
		partial bool
	)

	for page := 1; page <= schedulePageLimit; page++ {
		vars := map[string]any{
			"page":    page,
			"perPage": schedulePerPage,
			// Both bounds are exclusive, so widen by a second on each side to
			// keep an episode airing exactly at midnight.
			"from": start.Unix() - 1,
			"to":   end.Unix(),
		}

		var parsed struct {
			Page struct {
				PageInfo struct {
					HasNextPage bool `json:"hasNextPage"`
				} `json:"pageInfo"`
				AiringSchedules []struct {
					Episode  int   `json:"episode"`
					AiringAt int64 `json:"airingAt"`
					Media    struct {
						ID    int  `json:"id"`
						Adult bool `json:"isAdult"`
						Title struct {
							Romaji  string `json:"romaji"`
							English string `json:"english"`
							Native  string `json:"native"`
						} `json:"title"`
						Synonyms   []string `json:"synonyms"`
						Format     string   `json:"format"`
						Status     string   `json:"status"`
						CoverImage struct {
							Large  string `json:"large"`
							Medium string `json:"medium"`
						} `json:"coverImage"`
					} `json:"media"`
				} `json:"airingSchedules"`
			} `json:"Page"`
		}

		if err := anilistPost(scheduleQuery, vars, &parsed); err != nil {
			// A later page failing is not worth throwing away the days already
			// collected: show what we have and mark the week partial.
			if len(out) > 0 {
				return out, true, nil
			}
			return nil, false, err
		}

		for _, s := range parsed.Page.AiringSchedules {
			m := s.Media

			cover := m.CoverImage.Large
			if cover == "" {
				cover = m.CoverImage.Medium
			}

			at := time.Unix(s.AiringAt, 0).Local()
			out = append(out, ScheduleEntry{
				AniListID: m.ID,
				// Romaji first, like the catalog: it is what the scrapers
				// index on, so the search that follows a click hits more.
				Title:    firstNonEmpty(m.Title.Romaji, m.Title.English, m.Title.Native),
				Romaji:   m.Title.Romaji,
				English:  m.Title.English,
				Cover:    cover,
				Episode:  s.Episode,
				AiringAt: s.AiringAt,
				Time:     at.Format("15:04"),
				Format:   m.Format,
				Status:   m.Status,
				// airingSchedules takes no isAdult argument the way media
				// does, so the flag is carried through and applied in
				// layOutWeek rather than filtered in the query.
				adult: m.Adult,
				// Synonyms exist only to recognise favorites, so they are
				// folded into the match keys here and go no further.
				matchKeys: matchKeysFor(append([]string{
					m.Title.Romaji, m.Title.English, m.Title.Native,
				}, m.Synonyms...)),
			})
		}

		if !parsed.Page.PageInfo.HasNextPage {
			break
		}
		if page == schedulePageLimit {
			partial = true
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].AiringAt < out[j].AiringAt
	})
	return out, partial, nil
}

// --- favorite matching ---------------------------------------------------

// Matching a calendar entry to a favorite is a name comparison, because the
// two sides come from different worlds: favorites are scraper results keyed by
// source URL, while the calendar is AniList metadata carrying no scraper
// identity at all. There is no shared ID to join on.
//
// The comparison is exact-after-normalisation rather than fuzzy. Normalisation
// strips the noise the PT-BR sources add ("Dublado", "Todos os Episódios"),
// folds accents and drops punctuation, so "SPY×FAMILY" and "Spy x Family
// Dublado" reduce to the same key. What it deliberately will not do is guess:
// a favorite saved as "Kimetsu no Yaiba 3rd Season" does not match AniList's
// "Kimetsu no Yaiba: Katanakaji no Sato-hen", and a near-miss like that is
// better left unmarked than wrongly starred.

// releaseNoise are the words the sources decorate a title with. They are
// dropped before comparison.
var releaseNoise = map[string]bool{
	"dublado": true, "dub": true, "dublagem": true,
	"legendado": true, "leg": true, "legenda": true, "sub": true,
	"todos": true, "os": true, "episodios": true, "episodio": true,
	"online": true, "completo": true, "assistir": true,
	"hd": true, "ptbr": true, "pt": true, "br": true,
}

// matchKeysFor turns a set of title variants into comparison keys, dropping
// blanks and duplicates.
func matchKeysFor(titles []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(titles))
	for _, t := range titles {
		k := matchKey(t)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// matchKey reduces a title to lowercase letters and digits, accents folded and
// release noise removed. Spaces go too, so "Spy x Family" and "SPY×FAMILY"
// land on the same key.
func matchKey(title string) string {
	var b strings.Builder
	for _, w := range strings.Fields(strings.ToLower(normalizeTitle(title))) {
		w = foldWord(w)
		if w == "" || releaseNoise[w] {
			continue
		}
		b.WriteString(w)
	}
	return b.String()
}

// foldWord strips accents and everything that is not a letter or a digit.
func foldWord(w string) string {
	var b strings.Builder
	for _, r := range w {
		r = foldRune(r)
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// foldRune maps the accented Latin letters the sources actually use down to
// ASCII. Anything else is returned unchanged — a Japanese title keeps its
// kana, which is exactly what native-title matching needs.
func foldRune(r rune) rune {
	switch r {
	case 'á', 'à', 'â', 'ã', 'ä', 'å':
		return 'a'
	case 'é', 'è', 'ê', 'ë':
		return 'e'
	case 'í', 'ì', 'î', 'ï':
		return 'i'
	case 'ó', 'ò', 'ô', 'õ', 'ö':
		return 'o'
	case 'ú', 'ù', 'û', 'ü':
		return 'u'
	case 'ç':
		return 'c'
	case 'ñ':
		return 'n'
	case '×':
		// AniList writes "SPY×FAMILY" with a multiplication sign, the sources
		// with a plain "x". Without this the two lose a letter each and stop
		// matching.
		return 'x'
	}
	return r
}

// favoriteMatchKeys returns the comparison keys of every bookmarked title.
func favoriteMatchKeys() map[string]bool {
	favs := Favorites()
	out := make(map[string]bool, len(favs))
	for _, f := range favs {
		if k := matchKey(f.Result.Name); k != "" {
			out[k] = true
		}
	}
	return out
}

// matchesFavorite reports whether any of the entry's title variants names a
// bookmarked title.
func matchesFavorite(e ScheduleEntry, favs map[string]bool) bool {
	if len(favs) == 0 {
		return false
	}
	for _, k := range e.matchKeys {
		if favs[k] {
			return true
		}
	}
	return false
}

// ScheduleItem converts a calendar entry into the shape the catalog search
// takes, so a click on the calendar goes through exactly the same
// multi-variant search as a click on a catalog card.
func ScheduleItem(e ScheduleEntry) BrowseItem {
	return BrowseItem{
		AniListID: e.AniListID,
		Title:     e.Title,
		Romaji:    e.Romaji,
		English:   e.English,
		Cover:     e.Cover,
		Format:    e.Format,
		Status:    e.Status,
	}
}
