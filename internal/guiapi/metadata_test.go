package guiapi

import "testing"

func TestFormatRelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		year, month, day   int
		season             string
		seasonYear         int
		wantISO, wantLabel string
	}{
		{"full date", 2019, 4, 6, "SPRING", 2019, "2019-04-06", "6 abr 2019"},
		{"year and month", 2019, 4, 0, "", 0, "2019-04", "abr 2019"},
		{"year with season", 2019, 0, 0, "SPRING", 2019, "2019", "Primavera 2019"},
		{"year only", 1998, 0, 0, "", 0, "1998", "1998"},
		{"season year only", 0, 0, 0, "FALL", 2021, "2021", "Outono 2021"},
		{"nothing known", 0, 0, 0, "", 0, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			iso, label := formatRelease(tt.year, tt.month, tt.day, tt.season, tt.seasonYear)
			if iso != tt.wantISO {
				t.Errorf("iso = %q, want %q", iso, tt.wantISO)
			}
			if label != tt.wantLabel {
				t.Errorf("label = %q, want %q", label, tt.wantLabel)
			}
		})
	}
}

// December is index 11 in monthNames; an off-by-one here would panic on
// real data rather than fail visibly, so pin both ends of the range.
func TestFormatReleaseMonthBounds(t *testing.T) {
	t.Parallel()

	if _, label := formatRelease(2020, 1, 1, "", 0); label != "1 jan 2020" {
		t.Fatalf("January label = %q", label)
	}
	if _, label := formatRelease(2020, 12, 31, "", 0); label != "31 dez 2020" {
		t.Fatalf("December label = %q", label)
	}
}

func TestTitleCase(t *testing.T) {
	t.Parallel()

	if got := titleCase("SPRING"); got != "Primavera" {
		t.Fatalf("titleCase = %q", got)
	}
	if got := titleCase(""); got != "" {
		t.Fatalf("titleCase on empty = %q", got)
	}
}

// An unknown title must degrade to whatever the scraper reported rather
// than returning nothing at all.
func TestGetTitleInfoFallsBackToScraperYear(t *testing.T) {
	info := GetTitleInfo(SearchResult{
		Name: "a title AniList will never have 9f3a2b7c1d",
		Year: "2011",
	})
	if info.Year != "2011" || info.ReleaseLabel != "2011" {
		t.Fatalf("GetTitleInfo = %+v, want the scraper year to survive", info)
	}
}
