package guiapi

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/alvarorichard/Goanime/internal/api/source"
	"github.com/alvarorichard/Goanime/internal/scraper/providers/superflix"
)

// warmUpTimeout bounds the readiness probe. It is short because WarmUp is
// deliberately cheap — it checks the environment, it does not solve.
const warmUpTimeout = 30 * time.Second

// Environment variables the SuperFlix solver reads at solve time. Setting
// them from the GUI is how the settings panel reaches the solver without
// threading options through the whole registry.
const (
	envHeadless = "GOANIME_SF_HEADLESS"
	envBundled  = "GOANIME_SF_BUNDLED"
	envChannel  = "GOANIME_SF_CHROME_CHANNEL"
)

// GateStatus describes whether a browser-gated source can run here, so the
// UI can say so before the user clicks play instead of after.
type GateStatus struct {
	// Source is the source kind this status is about.
	Source string `json:"source"`
	// Gated is false for sources that need no browser at all.
	Gated bool `json:"gated"`
	// Ready is true when nothing blocks a solve in this environment.
	Ready bool `json:"ready"`
	// SetupPending is true on first run, before the helper browser exists.
	// Clearing the gate then also downloads Chromium, which takes a while.
	SetupPending bool `json:"setupPending"`
	// NoDisplay is true when there is no screen to show the browser on and
	// the user has not opted into headless.
	NoDisplay bool `json:"noDisplay"`
	// Message is plain-language guidance for the user.
	Message string `json:"message"`
}

// GateOptions mirrors the solver knobs that already exist as environment
// variables, so the GUI can offer them as settings.
type GateOptions struct {
	// Headless runs the helper browser with no visible window. Cloudflare
	// usually rejects that, so it is an escape hatch for screenless hosts,
	// not a default.
	Headless bool `json:"headless"`
	// Bundled forces Playwright's own Chromium instead of system Chrome.
	Bundled bool `json:"bundled"`
	// Channel pins a browser distribution ("chrome", "msedge", …).
	// Empty means auto.
	Channel string `json:"channel"`
}

// gateMu guards the environment writes below. os.Setenv is process-global,
// and the settings panel can fire while a solve is being set up.
var gateMu sync.Mutex

// GetGateOptions reports the solver knobs currently in effect.
func GetGateOptions() GateOptions {
	gateMu.Lock()
	defer gateMu.Unlock()

	return GateOptions{
		Headless: os.Getenv(envHeadless) != "",
		Bundled:  os.Getenv(envBundled) != "",
		Channel:  os.Getenv(envChannel),
	}
}

// SetGateOptions applies the solver knobs. They take effect on the next
// solve: the solver reads its configuration from the environment each time,
// so there is nothing to restart.
func SetGateOptions(opts GateOptions) error {
	gateMu.Lock()
	defer gateMu.Unlock()

	if err := setFlagEnv(envHeadless, opts.Headless); err != nil {
		return err
	}
	if err := setFlagEnv(envBundled, opts.Bundled); err != nil {
		return err
	}

	channel := strings.TrimSpace(opts.Channel)
	if channel == "" {
		return os.Unsetenv(envChannel)
	}
	return os.Setenv(envChannel, channel)
}

// setFlagEnv writes a boolean knob as the "set or absent" convention the
// solver's config loader expects (it tests for a non-empty value).
func setFlagEnv(name string, on bool) error {
	if on {
		return os.Setenv(name, "1")
	}
	return os.Unsetenv(name)
}

// GetGateStatus reports whether the named source's bot check can be cleared
// in this environment. Sources that are not browser-gated come back with
// Gated false and Ready true, so the frontend needs no special case.
func GetGateStatus(sourceName string) GateStatus {
	st := GateStatus{Source: sourceName}

	src := findSource(sourceName)
	if src == nil || !source.IsBrowserGated(src) {
		st.Ready = true
		return st
	}

	st.Gated = true
	st.NoDisplay = superflix.HeadlessEnvironment()
	st.SetupPending = superflix.BrowserSetupPending()

	switch {
	case st.NoDisplay:
		st.Message = "Esta fonte precisa abrir uma janela de navegador para provar que você não é um robô, " +
			"mas nenhuma tela foi encontrada. Rode o GoAnime no computador, ou ligue a opção " +
			"\"esconder a janela\" nas configurações para tentar mesmo assim."
	case st.SetupPending:
		st.Ready = true
		st.Message = "Primeira vez aqui: um navegador auxiliar será baixado quando você " +
			"assistir algo pela primeira vez. Isso acontece uma vez só e precisa de internet."
	default:
		st.Ready = true
		st.Message = "Tudo pronto. Uma janela de navegador pode aparecer rapidamente na primeira vez — " +
			"deixe terminar; depois disso a verificação fica salva."
	}
	return st
}

// findSource resolves a display name to a registered source.
func findSource(name string) source.Source {
	for _, s := range source.ActiveSources() {
		kind := string(s.Describe().Kind)
		if strings.EqualFold(kind, name) || strings.HasPrefix(strings.ToLower(name), strings.ToLower(kind)) {
			return s
		}
	}
	return nil
}

// PrepareSource runs the source's warm-up ahead of playback. For a
// browser-gated source this is the check that would otherwise run on the
// first play — doing it here means the user learns about a screenless host
// or a missing browser at a moment they chose, not halfway through starting
// an episode.
//
// It deliberately does NOT force a solve: the solver clears the gate as part
// of the first real request and caches the result in its persistent browser
// profile, so a separate eager solve would just open a second window.
func PrepareSource(sourceName string) (GateStatus, error) {
	src := findSource(sourceName)
	if src == nil {
		return GateStatus{Source: sourceName, Ready: true}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), warmUpTimeout)
	defer cancel()

	if err := source.WarmUp(ctx, src); err != nil {
		st := GetGateStatus(sourceName)
		st.Ready = false
		st.Message = err.Error()
		return st, fmt.Errorf("%s não está pronto: %w", sourceName, err)
	}
	return GetGateStatus(sourceName), nil
}
