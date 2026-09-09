package guiapi

import (
	"context"
	"strings"
	"sync"

	"github.com/alvarorichard/Goanime/internal/storage"
	"github.com/alvarorichard/Goanime/internal/util"
)

// AnimeFire rebuilt its site and retired the /animes/<slug>-todos-os-episodios
// URLs. Those URLs are the identity of every AnimeFire entry in the library —
// they are what titleKey hashes — so a favorite or a resume point saved before
// the rewrite still lists, but no longer resolves to anything playable.
//
// Re-resolving them is a title search per entry against the new API, so it
// runs once, in the background, and only rewrites an entry when the match is
// unambiguous. An entry that cannot be matched is left exactly as it was: the
// scraper already fails it with an explanation telling the user to search the
// title again, which is a better outcome than repointing a bookmark at the
// wrong anime.

// RepairReport says what one repair pass did.
type RepairReport struct {
	// Checked is how many stale entries were found.
	Checked int `json:"checked"`
	// Repaired is how many were re-resolved and rewritten.
	Repaired int `json:"repaired"`
	// Skipped is how many had no confident match and were left alone.
	Skipped int `json:"skipped"`
}

var repairOnce sync.Once

// ensureLibraryRepair kicks off one background repair pass per process. It is
// called from the library entry points, so it happens when the library is
// actually used rather than on every launch.
func ensureLibraryRepair() {
	repairOnce.Do(func() {
		go func() {
			defer func() {
				// A repair is a convenience; it must never take the app down.
				if r := recover(); r != nil {
					util.Debug("library repair panicked", "recover", r)
				}
			}()
			report := RepairLibrary()
			if report.Checked > 0 {
				util.Debug("library repair finished",
					"checked", report.Checked,
					"repaired", report.Repaired,
					"skipped", report.Skipped)
			}
		}()
	})
}

// legacyAnimeFireURL reports whether a stored URL uses AnimeFire's retired
// /animes/<slug> format. The live site serves /anime/<id> instead.
func legacyAnimeFireURL(raw string) bool {
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "animefire") && strings.Contains(lower, "/animes/")
}

// titleFromLegacySlug recovers a searchable title from a retired URL, e.g.
// ".../animes/love-hina-todos-os-episodios" gives "love hina". It is the
// second opinion used to confirm a match, so a stored name that drifted from
// the source's spelling cannot repoint an entry on its own.
func titleFromLegacySlug(raw string) string {
	lower := strings.ToLower(raw)
	idx := strings.LastIndex(lower, "/animes/")
	if idx < 0 {
		return ""
	}
	slug := lower[idx+len("/animes/"):]
	if cut := strings.IndexAny(slug, "?#"); cut >= 0 {
		slug = slug[:cut]
	}
	slug = strings.Trim(slug, "/")

	// Strip the decorations the old slugs carried.
	for _, suffix := range []string{
		"-todos-os-episodios", "-todos-episodios", "-online", "-hd", "-completo",
	} {
		slug = strings.TrimSuffix(slug, suffix)
	}

	return strings.TrimSpace(strings.ReplaceAll(slug, "-", " "))
}

// seasonOrdinal extracts the season a title pins itself to — "Youjo Senki
// II" gives "2", "Tensei ... 4th Season" gives "4" — and "" when the title
// names no particular season.
//
// It exists because of how the rewritten catalog answers a sequel's name: a
// search for "Youjo Senki II" ranks "Saga of Tanya the Evil" first, which is
// season one. Accepting it would move a bookmark to the wrong season, so a
// candidate has to agree about the ordinal before it can be trusted.
func seasonOrdinal(title string) string {
	words := strings.Fields(strings.ToLower(normalizeTitle(title)))

	for i, w := range words {
		w = strings.Trim(w, "()[].,:-")
		switch w {
		case "ii", "2nd", "second":
			return "2"
		case "iii", "3rd", "third":
			return "3"
		case "iv", "4th", "fourth":
			return "4"
		case "v", "5th", "fifth":
			return "5"
		}
		// "season 2" and a bare trailing "2".
		if w == "season" || w == "temporada" {
			if i+1 < len(words) {
				if d := strings.Trim(words[i+1], "()[].,:-"); isSmallOrdinal(d) {
					return d
				}
			}
			continue
		}
		if i == len(words)-1 && isSmallOrdinal(w) {
			return w
		}
	}
	return ""
}

// isSmallOrdinal reports whether w is a plausible season number.
func isSmallOrdinal(w string) bool {
	switch w {
	case "2", "3", "4", "5", "6", "7", "8", "9":
		return true
	}
	return false
}

// isTruncationOf reports whether one key is contained in the other. It is how
// "Monogatari" comes back as the best hit for "Bakemonogatari": a shorter
// title that merely ends the same way is a different anime, not a match.
func isTruncationOf(a, b string) bool {
	if a == "" || b == "" || a == b {
		return false
	}
	return strings.Contains(a, b) || strings.Contains(b, a)
}

// resolveLegacyAnimeFire searches the live source for a retired entry and
// returns its new URL, or "" when no candidate can be trusted.
//
// An exact title match is preferred, but it rarely happens any more: the
// rewritten catalog is titled in Portuguese and English where the old one
// used romaji, so the library's "Sousou no Frieren" now lives under "Frieren
// e a Jornada para o Além" and no amount of string comparison will connect
// them. What does connect them is the source's own search ranking, which
// still finds the right anime from the romaji name.
//
// So the best hit is accepted, with two guards that only ever reject — both
// taken from the cases where the ranking is genuinely misleading:
//
//   - a title pinned to a season must match that season, or a bookmark for
//     "Youjo Senki II" would move to season one;
//   - a candidate that is merely a substring of what was asked for is a
//     different title, which is what makes "Monogatari" the top hit for
//     "Bakemonogatari".
//
// Anything those reject is left broken on purpose. The scraper fails such an
// entry with a message telling the user to search the title again, which is
// recoverable; a bookmark silently pointing at the wrong anime is not.
func resolveLegacyAnimeFire(name, oldURL string) string {
	slugTitle := titleFromLegacySlug(oldURL)
	query := strings.TrimSpace(normalizeTitle(name))
	if query == "" {
		query = slugTitle
	}
	if query == "" {
		return ""
	}

	results, err := searchAnimeFireFor(query)
	if err != nil {
		return ""
	}

	live := make([]SearchResult, 0, len(results))
	for _, r := range results {
		if !legacyAnimeFireURL(r.URL) && strings.TrimSpace(r.URL) != "" {
			live = append(live, r)
		}
	}
	if len(live) == 0 {
		return ""
	}

	// An exact match anywhere in the results beats the ranking.
	wanted := map[string]bool{}
	for _, candidate := range []string{name, slugTitle} {
		if k := matchKey(candidate); k != "" {
			wanted[k] = true
		}
	}
	for _, r := range live {
		if wanted[matchKey(r.Name)] {
			return r.URL
		}
	}

	// Otherwise trust the ranking, subject to the guards.
	best := live[0]
	wantSeason := seasonOrdinal(name)
	if wantSeason == "" {
		wantSeason = seasonOrdinal(slugTitle)
	}
	if seasonOrdinal(best.Name) != wantSeason {
		return ""
	}

	bestKey := matchKey(best.Name)
	for k := range wanted {
		if isTruncationOf(k, bestKey) {
			return ""
		}
	}
	return best.URL
}

// searchAnimeFireFor is the search indirection, so tests can resolve without
// touching the network.
var searchAnimeFireFor = func(query string) ([]SearchResult, error) {
	return Search(query, "animefire")
}

// RepairLibrary re-resolves every library entry whose AnimeFire URL was
// retired, rewriting favorites and watch history to the new identity.
//
// It is safe to run more than once: an entry that already resolves is not
// stale, and one that cannot be matched is skipped rather than dropped.
func RepairLibrary() RepairReport {
	ensureMigration()

	ctx := context.Background()
	store := storage.Default()
	var report RepairReport

	// resolved memoises one search per distinct title: a series with twelve
	// watched episodes is one lookup, not twelve.
	resolved := map[string]string{}
	resolve := func(name, oldURL string) string {
		if url, ok := resolved[oldURL]; ok {
			return url
		}
		url := resolveLegacyAnimeFire(name, oldURL)
		resolved[oldURL] = url
		return url
	}

	report.mergeFavorites(ctx, store, resolve)
	report.mergeHistory(ctx, store, resolve)
	return report
}

// mergeFavorites rewrites the bookmarks whose URL was retired.
func (report *RepairReport) mergeFavorites(
	ctx context.Context, store *storage.Storage,
	resolve func(name, oldURL string) string,
) {
	favs, err := store.ListFavorites(ctx)
	if err != nil {
		return
	}

	for _, f := range favs {
		if !legacyAnimeFireURL(f.URL) {
			continue
		}
		report.Checked++

		newURL := resolve(f.Name, f.URL)
		if newURL == "" {
			report.Skipped++
			continue
		}

		updated := f
		updated.URL = newURL
		updated.MediaID = titleKey(SearchResult{Source: f.Source, URL: newURL, Name: f.Name})

		if updated.MediaID == f.MediaID {
			continue
		}
		// Write the new identity before dropping the old one, so an
		// interruption leaves a duplicate rather than a lost bookmark.
		if err := store.AddFavorite(ctx, updated); err != nil {
			report.Skipped++
			continue
		}
		_ = store.RemoveFavorite(ctx, f.MediaID)
		report.Repaired++
	}
}

// mergeHistory rewrites the watch history rows that share a retired identity.
// Every episode of a title moves together, so a resume point survives.
func (report *RepairReport) mergeHistory(
	ctx context.Context, store *storage.Storage,
	resolve func(name, oldURL string) string,
) {
	entries, err := store.GetAllHistory(ctx, historyLimit)
	if err != nil {
		return
	}

	// The media id is the old URL prefixed by its source, so group by it and
	// resolve each distinct title once.
	byMedia := map[string][]storage.MediaProgress{}
	order := make([]string, 0)
	for _, e := range entries {
		if !legacyAnimeFireURL(e.MediaID) {
			continue
		}
		if _, seen := byMedia[e.MediaID]; !seen {
			order = append(order, e.MediaID)
		}
		byMedia[e.MediaID] = append(byMedia[e.MediaID], e)
	}

	for _, oldID := range order {
		rows := byMedia[oldID]
		report.Checked++

		newURL := resolve(rows[0].Title, oldID)
		if newURL == "" {
			report.Skipped++
			continue
		}

		newID := titleKey(SearchResult{Source: rows[0].Source, URL: newURL, Name: rows[0].Title})
		if newID == oldID {
			continue
		}

		// Re-save every episode under the new id first; only then drop the
		// old rows, so a failure halfway cannot lose progress.
		moved := true
		for _, row := range rows {
			row.MediaID = newID
			if err := store.SaveProgress(ctx, row); err != nil {
				moved = false
				break
			}
		}
		if !moved {
			report.Skipped++
			continue
		}

		_ = store.DeleteProgress(ctx, oldID)
		report.Repaired++
	}
}
