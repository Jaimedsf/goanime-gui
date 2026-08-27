package guiapi

import (
	"testing"
	"time"
)

// December is the case that gets this wrong: AniList counts a December
// premiere as the *next* year's winter, so returning the current year there
// would show a season that ended eleven months ago.
func TestSeasonForCoversEveryMonth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		month      int
		wantYear   int
		wantSeason string
	}{
		{1, 2024, SeasonWinter},
		{2, 2024, SeasonWinter},
		{3, 2024, SeasonSpring},
		{4, 2024, SeasonSpring},
		{5, 2024, SeasonSpring},
		{6, 2024, SeasonSummer},
		{7, 2024, SeasonSummer},
		{8, 2024, SeasonSummer},
		{9, 2024, SeasonFall},
		{10, 2024, SeasonFall},
		{11, 2024, SeasonFall},
		{12, 2025, SeasonWinter}, // rolls into next year
	}

	for _, tt := range tests {
		year, season := seasonFor(2024, tt.month)
		if year != tt.wantYear || season != tt.wantSeason {
			t.Errorf("seasonFor(2024, %d) = (%d, %s), want (%d, %s)",
				tt.month, year, season, tt.wantYear, tt.wantSeason)
		}
	}
}

func TestCurrentSeasonAgreesWithSeasonFor(t *testing.T) {
	t.Parallel()

	now := time.Now()
	wantYear, wantSeason := seasonFor(now.Year(), int(now.Month()))

	year, season := CurrentSeason()
	if year != wantYear || season != wantSeason {
		t.Fatalf("CurrentSeason() = (%d, %s), want (%d, %s)",
			year, season, wantYear, wantSeason)
	}
}

func TestValidSeasonNormalises(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"WINTER":   SeasonWinter,
		"spring":   SeasonSpring,
		" Summer ": SeasonSummer,
		"FALL":     SeasonFall,
		"AUTUMN":   SeasonFall, // AniList says FALL; accept the synonym
	}

	for in, want := range cases {
		if got := validSeason(in); got != want {
			t.Errorf("validSeason(%q) = %q, want %q", in, got, want)
		}
	}

	// Anything unrecognised falls back to the season airing now rather than
	// erroring, so the frontend can call Browse with no arguments.
	_, current := CurrentSeason()
	for _, bad := range []string{"", "   ", "nonsense", "MONSOON"} {
		if got := validSeason(bad); got != current {
			t.Errorf("validSeason(%q) = %q, want the current season %q", bad, got, current)
		}
	}
}

func TestSeasonOptionsArePortugueseAndComplete(t *testing.T) {
	t.Parallel()

	opts := SeasonOptions()
	if len(opts) != 4 {
		t.Fatalf("SeasonOptions() returned %d entries, want 4", len(opts))
	}

	want := map[string]string{
		SeasonWinter: "Inverno",
		SeasonSpring: "Primavera",
		SeasonSummer: "Verão",
		SeasonFall:   "Outono",
	}

	for _, o := range opts {
		if want[o.Value] != o.Label {
			t.Errorf("season %q labelled %q, want %q", o.Value, o.Label, want[o.Value])
		}
	}
}

// The picker must reach one year ahead (announced seasons) and be ordered
// newest-first, which is what the dropdown relies on for its default.
func TestYearOptionsRangeAndOrder(t *testing.T) {
	t.Parallel()

	years := YearOptions()
	if len(years) < 2 {
		t.Fatalf("YearOptions() returned %d entries", len(years))
	}

	current, _ := CurrentSeason()
	if years[0] != current+1 {
		t.Errorf("first year = %d, want next year %d", years[0], current+1)
	}
	if years[len(years)-1] != 1960 {
		t.Errorf("last year = %d, want 1960", years[len(years)-1])
	}

	for i := 1; i < len(years); i++ {
		if years[i] >= years[i-1] {
			t.Fatalf("years are not strictly descending at %d: %d then %d",
				i, years[i-1], years[i])
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()

	if got := firstNonEmpty("", "  ", "romaji", "english"); got != "romaji" {
		t.Fatalf("firstNonEmpty = %q", got)
	}
	if got := firstNonEmpty("", "   "); got != "" {
		t.Fatalf("all-blank input should give an empty string, got %q", got)
	}
}

// Browse normalises its arguments rather than erroring, so a zero year and
// a blank season resolve to the season airing now. This does hit the
// network, so a failure here is tolerated — the assertion is about the
// normalisation, not about AniList being reachable.
func TestBrowseNormalisesArguments(t *testing.T) {
	page, err := Browse(BrowseQuery{})
	if err != nil {
		t.Skipf("AniList unreachable, skipping: %v", err)
	}

	wantYear, wantSeason := CurrentSeason()
	if page.Query.Year != wantYear || page.Query.Season != wantSeason {
		t.Errorf("the zero query resolved to %d/%s, want %d/%s",
			page.Query.Year, page.Query.Season, wantYear, wantSeason)
	}
	if page.Page != 1 {
		t.Errorf("page = %d, want the request to be clamped to 1", page.Page)
	}
	if page.Label == "" {
		t.Error("a catalog page must carry a display label")
	}
}

// normalise is where a stale dropdown value could silently narrow results,
// so pin its decisions without hitting the network.
func TestNormaliseQuery(t *testing.T) {
	t.Parallel()

	curYear, curSeason := CurrentSeason()

	t.Run("zero query means the current season", func(t *testing.T) {
		got := BrowseQuery{}.normalise()
		if got.Mode != ModeSeason || got.Year != curYear || got.Season != curSeason {
			t.Fatalf("normalise() = %+v", got)
		}
		if got.Page != 1 {
			t.Fatalf("page = %d, want 1", got.Page)
		}
	})

	t.Run("season is dropped outside season mode", func(t *testing.T) {
		got := BrowseQuery{Mode: ModeTop, Season: SeasonSpring}.normalise()
		if got.Season != "" {
			t.Fatalf("season %q survived into %s mode", got.Season, got.Mode)
		}
	})

	t.Run("any year is allowed outside season mode", func(t *testing.T) {
		got := BrowseQuery{Mode: ModePopular}.normalise()
		if got.Year != 0 {
			t.Fatalf("year = %d, want 0 (any year)", got.Year)
		}
	})

	t.Run("an unknown mode falls back to season", func(t *testing.T) {
		if got := (BrowseQuery{Mode: "nonsense"}).normalise(); got.Mode != ModeSeason {
			t.Fatalf("mode = %q, want %q", got.Mode, ModeSeason)
		}
	})

	t.Run("format is upper-cased", func(t *testing.T) {
		if got := (BrowseQuery{Mode: ModeTop, Format: "movie"}).normalise(); got.Format != "MOVIE" {
			t.Fatalf("format = %q", got.Format)
		}
	})
}

// Two different queries must not share a cache entry, or switching a filter
// would show the previous listing.
func TestCacheKeyDistinguishesQueries(t *testing.T) {
	t.Parallel()

	base := BrowseQuery{Mode: ModeTop, Year: 2020, Page: 1}.normalise()
	variants := []BrowseQuery{
		{Mode: ModePopular, Year: 2020, Page: 1},
		{Mode: ModeTop, Year: 2021, Page: 1},
		{Mode: ModeTop, Year: 2020, Page: 2},
		{Mode: ModeTop, Year: 2020, Genre: "Action", Page: 1},
		{Mode: ModeTop, Year: 2020, Format: "MOVIE", Page: 1},
	}

	for _, v := range variants {
		if v.normalise().cacheKey() == base.cacheKey() {
			t.Errorf("%+v collides with the base query key %q", v, base.cacheKey())
		}
	}
}

func TestModeAndFormatOptionsArePopulated(t *testing.T) {
	t.Parallel()

	modes := ModeOptions()
	if len(modes) < 5 {
		t.Fatalf("ModeOptions() returned %d entries", len(modes))
	}
	if modes[0].Value != ModeSeason {
		t.Errorf("first mode = %q, want %q", modes[0].Value, ModeSeason)
	}
	for _, m := range modes {
		if m.Label == "" {
			t.Errorf("mode %q has no label", m.Value)
		}
	}

	formats := FormatOptions()
	if formats[0].Value != "" {
		t.Errorf("the format list must lead with an \"any\" entry, got %q", formats[0].Value)
	}
}

// Every catalog query sends isAdult:false, so Hentai is excluded from the
// results anyway and offering it as a filter would only ever produce empty
// pages.
func TestGenreOptionsLeadWithAnyAndExcludeAdult(t *testing.T) {
	t.Parallel()

	genres := GenreOptions()
	if len(genres) < 2 {
		t.Fatalf("GenreOptions returned %d entries", len(genres))
	}
	if genres[0].Value != "" {
		t.Errorf("the genre list must lead with an \"any\" entry, got %q", genres[0].Value)
	}
	for _, g := range genres {
		if g.Value == "Hentai" {
			t.Error("Hentai must not be offered: isAdult:false filters it out anyway")
		}
		if g.Label == "" {
			t.Errorf("genre %q has no label", g.Value)
		}
	}
}

// The fallback list is used when AniList cannot be reached, and it must not
// smuggle back the entry GenreOptions is there to strip.
func TestFallbackGenresOfferNoHentai(t *testing.T) {
	t.Parallel()

	for _, g := range fallbackGenres() {
		if g == "Hentai" {
			t.Error("fallbackGenres must not offer Hentai")
		}
	}
}

// An unknown genre must survive untranslated rather than vanish, so a genre
// added by AniList still works.
func TestGenreLabelFallsBackToTheOriginal(t *testing.T) {
	t.Parallel()

	if got := genreLabel("Action"); got != "Ação" {
		t.Errorf("genreLabel(\"Action\") = %q", got)
	}
	if got := genreLabel("Brand New Genre"); got != "Brand New Genre" {
		t.Errorf("an unknown genre should pass through, got %q", got)
	}
}

func TestLabelForDescribesTheQuery(t *testing.T) {
	t.Parallel()

	season := BrowseQuery{Mode: ModeSeason, Year: 2024, Season: SeasonSpring}.normalise()
	if got := labelFor(season); got != "Primavera 2024" {
		t.Errorf("season label = %q", got)
	}

	filtered := BrowseQuery{Mode: ModeTop, Year: 1998, Genre: "Action"}.normalise()
	if got := labelFor(filtered); got != "Melhores notas · 1998 · Ação" {
		t.Errorf("filtered label = %q", got)
	}

	plain := BrowseQuery{Mode: ModeTrending}.normalise()
	if got := labelFor(plain); got != "Em alta agora" {
		t.Errorf("plain label = %q", got)
	}
}

func TestStatusForOnlyAppliesToTimedModes(t *testing.T) {
	t.Parallel()

	if got := statusFor(ModeUpcoming); got != "NOT_YET_RELEASED" {
		t.Errorf("upcoming status = %q", got)
	}
	if got := statusFor(ModeAiring); got != "RELEASING" {
		t.Errorf("airing status = %q", got)
	}
	for _, m := range []string{ModeSeason, ModePopular, ModeTop, ModeTrending} {
		if got := statusFor(m); got != "" {
			t.Errorf("mode %q should not pin a status, got %q", m, got)
		}
	}
}
