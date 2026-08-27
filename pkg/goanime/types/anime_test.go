package types

import (
	"testing"

	"github.com/alvarorichard/Goanime/internal/models"
	"github.com/alvarorichard/Goanime/internal/scraper"
)

// These conversions are the seam between the internal models and the public
// API, so a mistake here is a wrong value handed to every consumer of the
// library rather than a crash somewhere visible. They are also the kind of
// code that looks obviously correct and is not: most of the functions below
// build *optional* nested structs, returning nil when every field is empty,
// and "empty" is defined differently for each one.

func TestFromInternalAnimeCopiesEveryField(t *testing.T) {
	t.Parallel()

	in := &models.Anime{
		Name:      "Kimetsu no Yaiba",
		URL:       "https://example.invalid/kny",
		ImageURL:  "https://example.invalid/kny.jpg",
		Source:    "AllAnime",
		AnilistID: 101922,
		MalID:     38000,
		Details: models.AniListDetails{
			ID:           101922,
			IDMal:        38000,
			Description:  "…",
			Genres:       []string{"Action", "Fantasy"},
			AverageScore: 83,
			Episodes:     26,
			Status:       "FINISHED",
			Title:        models.Title{Romaji: "Kimetsu no Yaiba", English: "Demon Slayer"},
			CoverImage:   models.CoverImages{Large: "large.jpg", Medium: "medium.jpg"},
		},
	}

	got := FromInternalAnime(in)
	if got == nil {
		t.Fatal("FromInternalAnime returned nil for a non-nil input")
	}

	if got.Name != in.Name || got.URL != in.URL || got.ImageURL != in.ImageURL {
		t.Errorf("identity fields not copied: %+v", got)
	}
	if got.AnilistID != in.AnilistID || got.MalID != in.MalID || got.Source != in.Source {
		t.Errorf("id/source fields not copied: %+v", got)
	}

	if got.Details == nil {
		t.Fatal("Details must always be built, even when empty")
	}
	if got.Details.AverageScore != 83 || got.Details.Episodes != 26 || got.Details.Status != "FINISHED" {
		t.Errorf("details not copied: %+v", got.Details)
	}
	if len(got.Details.Genres) != 2 || got.Details.Genres[0] != "Action" {
		t.Errorf("genres not copied: %v", got.Details.Genres)
	}
	if got.Details.Title == nil || got.Details.Title.English != "Demon Slayer" {
		t.Errorf("title not copied: %+v", got.Details.Title)
	}
	if got.Details.CoverImage == nil || got.Details.CoverImage.Large != "large.jpg" {
		t.Errorf("cover not copied: %+v", got.Details.CoverImage)
	}
}

// nil in, nil out. The client returns this straight to callers, so turning a
// missing anime into a zero-valued one would report a result that does not
// exist.
func TestFromInternalAnimeIsNilSafe(t *testing.T) {
	t.Parallel()

	if got := FromInternalAnime(nil); got != nil {
		t.Errorf("FromInternalAnime(nil) = %+v, want nil", got)
	}
	if got := FromInternalEpisode(nil); got != nil {
		t.Errorf("FromInternalEpisode(nil) = %+v, want nil", got)
	}
}

// Title and CoverImage are pointers so a caller can tell "AniList had no
// title" from "the title is blank". An anime the scrapers found but AniList
// did not must therefore come back with both nil, not with empty structs.
func TestFromInternalAnimeLeavesUnknownDetailsNil(t *testing.T) {
	t.Parallel()

	got := FromInternalAnime(&models.Anime{Name: "Obscure OVA"})

	if got.Details == nil {
		t.Fatal("Details itself is not optional")
	}
	if got.Details.Title != nil {
		t.Errorf("Title = %+v, want nil when AniList carried no title", got.Details.Title)
	}
	if got.Details.CoverImage != nil {
		t.Errorf("CoverImage = %+v, want nil when AniList carried no cover", got.Details.CoverImage)
	}
}

// Half a title is still a title: the English name alone is enough to build
// the struct, because dropping it would lose the only name AniList had.
func TestFromInternalAnimeKeepsAPartialTitle(t *testing.T) {
	t.Parallel()

	in := &models.Anime{Details: models.AniListDetails{
		Title:      models.Title{English: "Demon Slayer"},
		CoverImage: models.CoverImages{Medium: "medium.jpg"},
	}}

	got := FromInternalAnime(in)
	if got.Details.Title == nil || got.Details.Title.Romaji != "" || got.Details.Title.English != "Demon Slayer" {
		t.Errorf("a title with only the English name must survive, got %+v", got.Details.Title)
	}
	if got.Details.CoverImage == nil || got.Details.CoverImage.Large != "" {
		t.Errorf("a cover with only the medium URL must survive, got %+v", got.Details.CoverImage)
	}
}

func TestFromInternalAnimeConvertsNestedEpisodes(t *testing.T) {
	t.Parallel()

	in := &models.Anime{
		Name: "Bleach",
		Episodes: []models.Episode{
			{Number: "1", Num: 1, URL: "ep1"},
			{Number: "2", Num: 2, URL: "ep2"},
		},
	}

	got := FromInternalAnime(in)
	if len(got.Episodes) != 2 {
		t.Fatalf("converted %d episodes, want 2", len(got.Episodes))
	}
	if got.Episodes[0].Number != "1" || got.Episodes[1].URL != "ep2" {
		t.Errorf("episodes converted out of order or incompletely: %+v", got.Episodes)
	}
}

func TestFromInternalEpisodeCopiesEveryField(t *testing.T) {
	t.Parallel()

	in := &models.Episode{
		Number:   "12",
		Num:      12,
		URL:      "https://example.invalid/ep12",
		Aired:    "2019-09-28",
		Duration: 1440,
		IsFiller: true,
		IsRecap:  true,
		Synopsis: "…",
		Title:    models.TitleDetails{Romaji: "R", English: "E", Japanese: "J"},
	}

	got := FromInternalEpisode(in)
	if got.Number != "12" || got.Num != 12 || got.URL != in.URL || got.Aired != in.Aired {
		t.Errorf("identity fields not copied: %+v", got)
	}
	if got.Duration != 1440 || !got.IsFiller || !got.IsRecap || got.Synopsis != "…" {
		t.Errorf("flags not copied: %+v", got)
	}
	if got.Title == nil || got.Title.Japanese != "J" {
		t.Errorf("title not copied: %+v", got.Title)
	}
}

// SkipTimes is built only when AniSkip actually returned an interval. All
// zeros means "no data", and materialising a struct of zeros there would
// have the player skip from 00:00 to 00:00 on every episode.
func TestFromInternalEpisodeOmitsEmptySkipTimes(t *testing.T) {
	t.Parallel()

	got := FromInternalEpisode(&models.Episode{Number: "1"})
	if got.SkipTimes != nil {
		t.Errorf("SkipTimes = %+v, want nil when AniSkip returned nothing", got.SkipTimes)
	}
	if got.Title != nil {
		t.Errorf("Title = %+v, want nil when the episode has no title", got.Title)
	}
}

// An ending-only result still has to produce the struct: AniSkip often knows
// the ED and not the OP, and requiring both would throw away the half it had.
func TestFromInternalEpisodeKeepsSkipTimesFromEitherHalf(t *testing.T) {
	t.Parallel()

	in := &models.Episode{SkipTimes: models.SkipTimes{Ed: models.Skip{Start: 1300, End: 1390}}}

	got := FromInternalEpisode(in)
	if got.SkipTimes == nil {
		t.Fatal("an ED-only result must still produce SkipTimes")
	}
	if got.SkipTimes.Ed == nil || got.SkipTimes.Ed.Start != 1300 || got.SkipTimes.Ed.End != 1390 {
		t.Errorf("ED not copied: %+v", got.SkipTimes.Ed)
	}
	if got.SkipTimes.Op == nil || got.SkipTimes.Op.Start != 0 || got.SkipTimes.Op.End != 0 {
		t.Errorf("Op must be present and zeroed, got %+v", got.SkipTimes.Op)
	}
}

func TestFromInternalListsPreserveLengthAndOrder(t *testing.T) {
	t.Parallel()

	animes := FromInternalAnimeList([]*models.Anime{
		{Name: "first"}, nil, {Name: "third"},
	})
	if len(animes) != 3 {
		t.Fatalf("converted %d animes, want 3", len(animes))
	}
	if animes[0].Name != "first" || animes[2].Name != "third" {
		t.Errorf("order not preserved: %+v", animes)
	}
	// A nil entry stays a nil entry rather than shifting the ones after it.
	if animes[1] != nil {
		t.Errorf("animes[1] = %+v, want nil", animes[1])
	}

	episodes := FromInternalEpisodeList([]models.Episode{{Number: "1"}, {Number: "2"}})
	if len(episodes) != 2 || episodes[1].Number != "2" {
		t.Errorf("episode list not converted faithfully: %+v", episodes)
	}

	if got := FromInternalAnimeList(nil); len(got) != 0 {
		t.Errorf("FromInternalAnimeList(nil) = %+v, want empty", got)
	}
	if got := FromInternalEpisodeList(nil); len(got) != 0 {
		t.Errorf("FromInternalEpisodeList(nil) = %+v, want empty", got)
	}
}

func TestSourceString(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   Source
		want string
	}{
		{SourceAllAnime, "AllAnime"},
		{SourceAnimeFire, "AnimeFire"},
		{Source(42), "Unknown"},
	} {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("Source(%d).String() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSourceToScraperType(t *testing.T) {
	t.Parallel()

	if got := SourceAllAnime.ToScraperType(); got != scraper.AllAnimeType {
		t.Errorf("SourceAllAnime = %v, want %v", got, scraper.AllAnimeType)
	}
	if got := SourceAnimeFire.ToScraperType(); got != scraper.AnimefireType {
		t.Errorf("SourceAnimeFire = %v, want %v", got, scraper.AnimefireType)
	}
	// An unknown value falls back rather than producing a zero scraper type
	// that would dispatch to nothing.
	if got := Source(42).ToScraperType(); got != scraper.AllAnimeType {
		t.Errorf("unknown source = %v, want the AllAnime fallback", got)
	}
}

// ParseSource is what a library consumer passes user input to, so every
// spelling the docs mention has to work and anything else has to be an
// error rather than a silent default.
func TestParseSourceAcceptsEverySpelling(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"AllAnime", "allanime", "all"} {
		got, err := ParseSource(in)
		if err != nil || got != SourceAllAnime {
			t.Errorf("ParseSource(%q) = %v, %v; want SourceAllAnime, nil", in, got, err)
		}
	}
	for _, in := range []string{"AnimeFire", "animefire", "fire"} {
		got, err := ParseSource(in)
		if err != nil || got != SourceAnimeFire {
			t.Errorf("ParseSource(%q) = %v, %v; want SourceAnimeFire, nil", in, got, err)
		}
	}
}

func TestParseSourceRejectsUnknown(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "ALLANIME", "goyabu", "AnimeFire "} {
		if _, err := ParseSource(in); err == nil {
			t.Errorf("ParseSource(%q) returned no error; an unrecognised source must not be accepted", in)
		}
	}
}
