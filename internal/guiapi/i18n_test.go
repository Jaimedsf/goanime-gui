package guiapi

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The GUI ships in Brazilian Portuguese. These tests guard the translation
// the way a linter would: they scan the shipped frontend and the Go strings
// the user can actually see, and fail on English that slipped back in.
//
// They check *markers* — words that are unmistakably English and would never
// appear in a pt-BR string — rather than trying to detect language, which
// would be unreliable and noisy.

// englishMarkers are whole words that must not appear in user-visible text.
// Loanwords that Brazilian Portuguese genuinely uses — "download", "player"
// — are deliberately absent: flagging them would be wrong, not strict.
var englishMarkers = []string{
	"Search", "Cancel", "Play", "Episode", "Season", "Settings",
	"Favorites", "Watched", "Failed", "Loading", "Finished", "Close",
	"Quality", "Sources", "Results", "History", "Save", "Back",
	"could not", "failed to", "unable to", "no results", "not installed",
}

// allowedInUI are strings that legitimately stay as they are: product
// names, loanwords in common Brazilian use, and technical identifiers.
// Matching is case-insensitive, so "player" covers "Player" too.
var allowedInUI = []string{
	"GoAnime", "download", "downloads", "player", "mpv", "VLC", "MPC-HC",
	"Windows Media Player", "Google Chrome", "Chrome Beta", "Microsoft Edge",
	"AniList", "Ctrl", "PT-BR", "SuperFlix", "AllAnime", "AnimeFire", "Goyabu",
}

// frontendDir locates the shipped frontend relative to this package.
func frontendDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "cmd", "goanime-gui", "frontend", "dist")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("frontend not found at %s: %v", dir, err)
	}
	return dir
}

// stripAllowed removes the legitimately-English tokens so they cannot
// trigger a marker match. Case-insensitive, because the same word appears
// capitalised in a heading and lower-case mid-sentence.
func stripAllowed(s string) string {
	for _, a := range allowedInUI {
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(a))
		s = re.ReplaceAllString(s, " ")
	}
	return s
}

// stripInterpolations removes ${...} from a JS template literal. What is
// inside is code — variable names like `results.length` — not text the user
// reads, and scanning it produces nothing but false positives.
var interpolationRe = regexp.MustCompile(`\$\{[^}]*\}`)

func stripInterpolations(s string) string {
	return interpolationRe.ReplaceAllString(s, " ")
}

// visibleHTMLText extracts the text a user reads from the page: element
// text plus the attributes that render as words (placeholder, title,
// aria-label). Tags, ids, classes and comments are excluded.
var (
	htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlTagRe     = regexp.MustCompile(`(?s)<[^>]*>`)
	uiAttrRe      = regexp.MustCompile(`(?:placeholder|title|aria-label)="([^"]*)"`)
)

func visibleHTMLText(html string) string {
	body := htmlCommentRe.ReplaceAllString(html, " ")

	var attrs strings.Builder
	for _, m := range uiAttrRe.FindAllStringSubmatch(body, -1) {
		attrs.WriteString(m[1])
		attrs.WriteString("\n")
	}

	return attrs.String() + "\n" + htmlTagRe.ReplaceAllString(body, " ")
}

func TestHTMLIsPortuguese(t *testing.T) {
	dir := frontendDir(t)

	raw, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)

	if !strings.Contains(html, `lang="pt-BR"`) {
		t.Error(`index.html must declare lang="pt-BR"`)
	}

	text := stripAllowed(visibleHTMLText(html))
	for _, marker := range englishMarkers {
		if containsWord(text, marker) {
			t.Errorf("index.html still shows the English word %q to the user", marker)
		}
	}
}

// jsUserString matches the string literals that reach the user: the
// arguments of setStatus/toast, and assignments to textContent, title and
// placeholder. Console warnings and comments are deliberately not scanned —
// those stay in English like the rest of the code.
var jsUserStringRe = regexp.MustCompile(
	`(?:setStatus\(|toast\(|\.textContent\s*=\s*|\.title\s*=\s*|\.placeholder\s*=\s*)` +
		"(?:`([^`]*)`|\"([^\"]*)\")")

func TestJSUserStringsArePortuguese(t *testing.T) {
	dir := frontendDir(t)

	raw, err := os.ReadFile(filepath.Join(dir, "main.js"))
	if err != nil {
		t.Fatalf("read main.js: %v", err)
	}

	for _, m := range jsUserStringRe.FindAllStringSubmatch(string(raw), -1) {
		lit := m[1]
		if lit == "" {
			lit = m[2]
		}
		clean := stripAllowed(stripInterpolations(lit))
		for _, marker := range englishMarkers {
			if containsWord(clean, marker) {
				t.Errorf("main.js shows the English word %q in: %q", marker, lit)
			}
		}
	}
}

// The strings the Go side hands to the UI go through the same check. These
// are the ones a user meets on an error, which is exactly when a stray
// English sentence is most confusing.
func TestGoUserFacingStringsArePortuguese(t *testing.T) {
	t.Parallel()

	var samples []string

	for _, q := range Qualities() {
		samples = append(samples, q.Label)
	}
	for _, s := range Sources() {
		samples = append(samples, s.Label)
	}
	for _, name := range []string{"SuperFlix", "AllAnime"} {
		samples = append(samples, GetGateStatus(name).Message)
	}

	// Error paths the user hits most often.
	if _, err := Search("   ", "all"); err != nil {
		samples = append(samples, err.Error())
	}
	if _, err := StartDownload(SearchResult{}, EpisodeResult{Number: "1"}, "best"); err != nil {
		samples = append(samples, err.Error())
	}
	if err := PlayWith("", "vlc", "AllAnime"); err != nil {
		samples = append(samples, err.Error())
	}
	if _, err := Play(""); err != nil {
		samples = append(samples, err.Error())
	}

	for _, s := range samples {
		clean := stripAllowed(s)
		for _, marker := range englishMarkers {
			if containsWord(clean, marker) {
				t.Errorf("Go string shows the English word %q in: %q", marker, s)
			}
		}
	}
}

// Month abbreviations are user-visible through ReleaseLabel, so they have to
// be Portuguese too.
func TestMonthNamesArePortuguese(t *testing.T) {
	t.Parallel()

	want := []string{"jan", "fev", "mar", "abr", "mai", "jun",
		"jul", "ago", "set", "out", "nov", "dez"}

	for i, w := range want {
		if monthNames[i] != w {
			t.Errorf("monthNames[%d] = %q, want %q", i, monthNames[i], w)
		}
	}
}

func TestSeasonNamesArePortuguese(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"WINTER": "Inverno",
		"SPRING": "Primavera",
		"SUMMER": "Verão",
		"FALL":   "Outono",
		"AUTUMN": "Outono",
	}

	for in, want := range cases {
		if got := titleCase(in); got != want {
			t.Errorf("titleCase(%q) = %q, want %q", in, got, want)
		}
	}
}

// containsWord reports whether text contains marker as a standalone word,
// case-insensitively. A substring match would fire on Portuguese words that
// happen to embed an English one.
func containsWord(text, marker string) bool {
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(marker) + `\b`)
	return re.MatchString(text)
}
