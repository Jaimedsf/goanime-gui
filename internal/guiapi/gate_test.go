package guiapi

import (
	"os"
	"testing"
)

// clearGateEnv isolates the solver knobs so these tests never inherit or
// leak the developer's own environment.
func clearGateEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{envHeadless, envBundled, envChannel} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestGateOptionsRoundTrip(t *testing.T) {
	clearGateEnv(t)

	if got := GetGateOptions(); got.Headless || got.Bundled || got.Channel != "" {
		t.Fatalf("a clean environment should yield zero options, got %+v", got)
	}

	want := GateOptions{Headless: true, Bundled: true, Channel: "msedge"}
	if err := SetGateOptions(want); err != nil {
		t.Fatalf("SetGateOptions: %v", err)
	}

	got := GetGateOptions()
	if got != want {
		t.Fatalf("GetGateOptions = %+v, want %+v", got, want)
	}
}

// Turning a knob off must remove the variable, not set it to "false": the
// solver's config loader only tests for a non-empty value, so "false" would
// read as ON.
func TestGateOptionsOffUnsetsTheVariable(t *testing.T) {
	clearGateEnv(t)

	if err := SetGateOptions(GateOptions{Headless: true}); err != nil {
		t.Fatalf("SetGateOptions: %v", err)
	}
	if os.Getenv(envHeadless) == "" {
		t.Fatal("headless should be set")
	}

	if err := SetGateOptions(GateOptions{Headless: false}); err != nil {
		t.Fatalf("SetGateOptions: %v", err)
	}
	if v, ok := os.LookupEnv(envHeadless); ok {
		t.Fatalf("headless should be unset, but is present as %q", v)
	}
	if GetGateOptions().Headless {
		t.Fatal("headless still reads as on")
	}
}

func TestGateOptionsBlankChannelIsUnset(t *testing.T) {
	clearGateEnv(t)

	if err := SetGateOptions(GateOptions{Channel: "chrome"}); err != nil {
		t.Fatalf("SetGateOptions: %v", err)
	}
	if err := SetGateOptions(GateOptions{Channel: "   "}); err != nil {
		t.Fatalf("SetGateOptions: %v", err)
	}
	if v, ok := os.LookupEnv(envChannel); ok {
		t.Fatalf("a blank channel should unset the variable, got %q", v)
	}
}

// A source with no bot check must report ready without any browser talk, so
// the frontend needs no special case for it.
func TestGateStatusUngatedSourceIsReady(t *testing.T) {
	st := GetGateStatus("AllAnime")
	if st.Gated {
		t.Fatal("AllAnime is not browser-gated")
	}
	if !st.Ready {
		t.Fatal("an ungated source should always be ready")
	}
}

func TestGateStatusUnknownSourceIsReady(t *testing.T) {
	st := GetGateStatus("NotARealSource")
	if st.Gated || !st.Ready {
		t.Fatalf("an unknown source should be ungated and ready, got %+v", st)
	}
}

// SuperFlix is the browser-gated source; whatever the environment, the
// status must carry guidance rather than an empty message.
func TestGateStatusSuperFlixIsGatedAndExplained(t *testing.T) {
	st := GetGateStatus("SuperFlix")
	if !st.Gated {
		t.Fatal("SuperFlix should report as browser-gated")
	}
	if st.Message == "" {
		t.Fatal("a gated source must explain itself to the user")
	}
}

func TestPrepareUnknownSourceSucceeds(t *testing.T) {
	st, err := PrepareSource("NotARealSource")
	if err != nil {
		t.Fatalf("PrepareSource on an unknown source should not error: %v", err)
	}
	if !st.Ready {
		t.Fatal("an unknown source should be reported ready")
	}
}

func TestFindSourceIsCaseInsensitive(t *testing.T) {
	if findSource("superflix") == nil {
		t.Fatal("findSource should match regardless of case")
	}
	// The scraper spells AnimeFire as "Animefire.io"; the prefix match in
	// findSource is what makes that resolve.
	if findSource("Animefire.io") == nil {
		t.Fatal(`findSource should resolve the scraper spelling "Animefire.io"`)
	}
}
