package guiapi

import (
	"testing"
	"time"
)

// Matching a calendar entry to a favorite is the whole point of the feature,
// and the two sides never spell a title the same way: the sources add
// "Dublado", AniList uses "×" and full-width punctuation. These are the shapes
// that must collapse onto one key — and the near-misses that must not.
func TestMatchKeyNormalises(t *testing.T) {
	t.Parallel()

	same := []struct {
		name string
		a, b string
	}{
		{"dub tag", "Spy x Family Dublado", "Spy x Family"},
		{"full-width cross", "SPY×FAMILY", "Spy x Family"},
		{"scraper tag", "Bleach [Dublado]", "BLEACH"},
		{"accents", "Kimetsu no Yaiba: Katanakaji no Satô-hen", "Kimetsu no Yaiba Katanakaji no Sato-hen"},
		{"episode noise", "One Piece - Todos os Episódios", "One Piece"},
		{"punctuation", "Re:Zero kara Hajimeru Isekai Seikatsu", "Re Zero kara Hajimeru Isekai Seikatsu"},
	}
	for _, tt := range same {
		if got, want := matchKey(tt.a), matchKey(tt.b); got != want {
			t.Errorf("%s: matchKey(%q) = %q, want %q (from %q)",
				tt.name, tt.a, got, want, tt.b)
		}
	}

	// Different shows must stay different. A season number is meaning, not
	// noise: marking season 1 because season 2 airs would be a lie.
	diff := []struct{ a, b string }{
		{"One Piece", "One Piece Film: Red"},
		{"Spy x Family Season 2", "Spy x Family"},
	}
	for _, tt := range diff {
		if matchKey(tt.a) == matchKey(tt.b) {
			t.Errorf("matchKey(%q) and matchKey(%q) collided on %q",
				tt.a, tt.b, matchKey(tt.a))
		}
	}

	if matchKey("   ") != "" {
		t.Errorf("matchKey of blank text = %q, want empty", matchKey("   "))
	}
}

// A title made entirely of release noise ("Dublado") must not produce an empty
// key that then matches every other empty key.
func TestMatchesFavoriteIgnoresEmptyKeys(t *testing.T) {
	t.Parallel()

	e := ScheduleEntry{matchKeys: matchKeysFor([]string{"", "Dublado"})}
	if matchesFavorite(e, map[string]bool{"": true}) {
		t.Error("an entry with no usable title matched a favorite")
	}
}

func TestMatchKeysForDropsDuplicates(t *testing.T) {
	t.Parallel()

	keys := matchKeysFor([]string{"BLEACH", "Bleach", "Bleach Dublado", "Naruto"})
	if len(keys) != 2 {
		t.Fatalf("matchKeysFor returned %d keys (%v), want 2", len(keys), keys)
	}
}

// The week runs Monday to Sunday, whichever day it is asked on. Getting this
// wrong on a Sunday is the easy mistake: time.Weekday counts Sunday as 0, so
// a naive offset puts Sunday at the *start* of the week that just began.
func TestWeekStartForIsAlwaysMonday(t *testing.T) {
	t.Parallel()

	// 2026-08-24 is a Monday; walk the whole week from it.
	monday := time.Date(2026, 8, 24, 0, 0, 0, 0, time.Local)
	for i := 0; i < 7; i++ {
		day := monday.AddDate(0, 0, i).Add(15 * time.Hour)
		if got := weekStartFor(day); !got.Equal(monday) {
			t.Errorf("weekStartFor(%s, a %s) = %s, want %s",
				day.Format("2006-01-02"), day.Weekday(),
				got.Format("2006-01-02"), monday.Format("2006-01-02"))
		}
	}

	// The Sunday that ends the week belongs to it; the Monday after starts a
	// new one.
	nextMonday := monday.AddDate(0, 0, 7)
	if got := weekStartFor(nextMonday); !got.Equal(nextMonday) {
		t.Errorf("weekStartFor(next Monday) = %s, want %s",
			got.Format("2006-01-02"), nextMonday.Format("2006-01-02"))
	}
}

// The calendar always has exactly seven columns, Monday first, even on days
// nothing airs — a grid that grows and shrinks would move under the cursor.
func TestLayOutWeekAlwaysHasSevenDays(t *testing.T) {
	t.Parallel()

	start := weekStartFor(time.Now())
	snap := &scheduleSnapshot{start: start, fetchedAt: time.Now()}

	week := layOutWeek(snap, start)
	if len(week.Days) != scheduleDays {
		t.Fatalf("week has %d days, want %d", len(week.Days), scheduleDays)
	}
	if week.Total != 0 {
		t.Errorf("empty snapshot produced Total = %d, want 0", week.Total)
	}

	if week.Days[0].Weekday != "Seg" {
		t.Errorf("first column is %q, want Monday (\"Seg\")", week.Days[0].Weekday)
	}
	if week.Days[6].Weekday != "Dom" {
		t.Errorf("last column is %q, want Sunday (\"Dom\")", week.Days[6].Weekday)
	}

	// Exactly one column is today, every column before it is past, and none
	// after it is.
	todays := 0
	seenToday := false
	for _, d := range week.Days {
		if d.Today {
			todays++
			seenToday = true
			if d.Past {
				t.Errorf("today (%s) is also marked past", d.Date)
			}
			continue
		}
		if d.Past == seenToday {
			t.Errorf("day %s: past=%v, but it comes %s today",
				d.Date, d.Past, map[bool]string{true: "after", false: "before"}[seenToday])
		}
	}
	if todays != 1 {
		t.Errorf("%d columns marked today, want exactly 1", todays)
	}

	// Entries is never nil, so the frontend can iterate it unconditionally.
	for _, d := range week.Days {
		if d.Entries == nil {
			t.Errorf("day %s has nil Entries", d.Date)
		}
	}
}

// Only the three days around today get a relative name; the rest must keep a
// weekday, or a Monday and a Friday would both read as an unlabelled column.
func TestDayLabelNamesOnlyTheDaysAround(t *testing.T) {
	t.Parallel()

	today := time.Date(2026, 8, 26, 0, 0, 0, 0, time.Local) // a Wednesday

	tests := []struct {
		offset       int
		wantLabel    string
		wantRelative bool
	}{
		{-1, "Ontem", true},
		{0, "Hoje", true},
		{1, "Amanhã", true},
		{-2, "Segunda", false},
		{2, "Sexta", false},
		{4, "Domingo", false},
	}

	for _, tt := range tests {
		label, relative := dayLabel(today.AddDate(0, 0, tt.offset), today)
		if label != tt.wantLabel || relative != tt.wantRelative {
			t.Errorf("dayLabel(today%+d) = (%q, %v), want (%q, %v)",
				tt.offset, label, relative, tt.wantLabel, tt.wantRelative)
		}
	}
}

// Entries land on the day their broadcast time falls on locally, and anything
// outside the window is dropped rather than folded into an edge day.
//
// The week used here is deliberately far in the future, so the assertions do
// not depend on what today happens to be — a test pinned to a real date passes
// until that date goes by.
func TestLayOutWeekPlacesEntriesOnTheirDay(t *testing.T) {
	t.Parallel()

	start := time.Date(2099, 1, 5, 0, 0, 0, 0, time.Local) // a Monday
	wednesday := start.AddDate(0, 0, 2).Add(22 * time.Hour)
	outside := start.AddDate(0, 0, 30)

	snap := &scheduleSnapshot{
		start:     start,
		fetchedAt: time.Now(),
		entries: []ScheduleEntry{
			{Title: "Wednesday", AiringAt: wednesday.Unix(), Episode: 3},
			{Title: "Next month", AiringAt: outside.Unix()},
		},
	}

	week := layOutWeek(snap, start)
	if week.Total != 1 {
		t.Fatalf("Total = %d, want 1 (the out-of-window entry must be dropped)", week.Total)
	}
	if len(week.Days[2].Entries) != 1 {
		t.Fatalf("Wednesday has %d entries, want 1", len(week.Days[2].Entries))
	}
	if got := week.Days[2].Entries[0].Title; got != "Wednesday" {
		t.Errorf("Wednesday's entry = %q, want \"Wednesday\"", got)
	}
	if week.Days[2].Entries[0].Aired {
		t.Error("an airing in 2099 was marked as already aired")
	}
}

// A week that has gone by is still laid out — it is what the Monday and
// Tuesday columns show on a Thursday — with every episode marked as aired.
func TestLayOutWeekMarksPastAiringsAsAired(t *testing.T) {
	t.Parallel()

	start := time.Date(2020, 1, 6, 0, 0, 0, 0, time.Local) // a Monday
	aired := start.AddDate(0, 0, 3).Add(19 * time.Hour)

	snap := &scheduleSnapshot{
		start:     start,
		fetchedAt: time.Now(),
		entries:   []ScheduleEntry{{Title: "Long gone", AiringAt: aired.Unix()}},
	}

	week := layOutWeek(snap, start)
	if len(week.Days[3].Entries) != 1 {
		t.Fatalf("Thursday has %d entries, want 1", len(week.Days[3].Entries))
	}
	if !week.Days[3].Entries[0].Aired {
		t.Error("a 2020 airing was not marked as aired")
	}
	if !week.Days[3].Past {
		t.Error("a day in 2020 was not marked past")
	}
}

// startOfDay must work in the user's timezone: time.Time.Truncate rounds in
// UTC and would land mid-day for most of the world.
func TestStartOfDayIsLocalMidnight(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("timezone database unavailable: %v", err)
	}

	got := startOfDay(time.Date(2026, 8, 26, 21, 40, 12, 0, loc))
	want := time.Date(2026, 8, 26, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("startOfDay = %v, want %v", got, want)
	}
}

func TestScheduleItemCarriesTitleVariants(t *testing.T) {
	t.Parallel()

	item := ScheduleItem(ScheduleEntry{
		AniListID: 21,
		Title:     "One Piece",
		Romaji:    "One Piece",
		English:   "ONE PIECE",
		Cover:     "https://example.invalid/cover.jpg",
	})

	// SearchTitles searches every distinct variant; losing one here would
	// silently narrow the search a calendar click runs.
	if len(titleVariants(item)) != 1 {
		t.Errorf("variants = %v, want the two spellings to collapse to one",
			titleVariants(item))
	}
	if item.AniListID != 21 || item.Cover == "" {
		t.Errorf("ScheduleItem dropped fields: %+v", item)
	}
}
