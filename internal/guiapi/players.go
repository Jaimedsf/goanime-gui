package guiapi

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/alvarorichard/Goanime/internal/api/source"
	"github.com/alvarorichard/Goanime/internal/player"
	"github.com/alvarorichard/Goanime/internal/util"
)

// playerStartGrace is how long we wait after spawning an external player
// before declaring success. Long enough to catch an immediate crash (bad
// path, missing codec), short enough not to stall the UI.
const playerStartGrace = 1500 * time.Millisecond

// resolvePlayerPath maps friendly aliases like "vlc" or "wmplayer" to the
// real executable on disk. On Windows these are rarely on PATH, so we probe
// the standard install locations before giving up. Returns the original
// value when nothing matches, letting exec.LookPath have a try.
func resolvePlayerPath(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "vlc":
		candidates := []string{
			`C:\Program Files\VideoLAN\VLC\vlc.exe`,
			`C:\Program Files (x86)\VideoLAN\VLC\vlc.exe`,
		}
		if runtime.GOOS != "windows" {
			candidates = append(candidates,
				"/usr/bin/vlc", "/usr/local/bin/vlc",
				"/Applications/VLC.app/Contents/MacOS/VLC")
		}
		return firstExisting(candidates, "vlc")

	case "mpc-hc", "mpc":
		return firstExisting([]string{
			`C:\Program Files\MPC-HC\mpc-hc64.exe`,
			`C:\Program Files\MPC-HC\mpc-hc.exe`,
			`C:\Program Files (x86)\MPC-HC\mpc-hc.exe`,
		}, "mpc-hc64")

	case "wmplayer":
		candidates := []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Windows Media Player", "wmplayer.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Windows Media Player", "wmplayer.exe"),
		}
		if sysRoot := os.Getenv("SystemRoot"); sysRoot != "" {
			candidates = append(candidates, filepath.Join(sysRoot, "System32", "wmplayer.exe"))
		}
		return firstExisting(candidates, "wmplayer")
	}

	return name
}

// firstExisting returns the first path that exists on disk, or fallback.
//
// gosec flags the Stat below as a path traversal (G703) because some of the
// candidates are built from os.Getenv("ProgramFiles") and friends. The taint
// is real but the finding is not: the candidate list is assembled here from
// hardcoded executable names joined onto environment variables the operating
// system sets, never from anything a user or a scraper supplies. It is also
// a read of file metadata and nothing else -- nothing is opened, written or
// executed on the strength of it. The caller only learns whether a media
// player is installed.
func firstExisting(candidates []string, fallback string) string {
	for _, p := range candidates {
		if p == "" {
			continue
		}
		// #nosec G703 -- see the note above: OS-provided paths, metadata read only.
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return fallback
}

// PlayerAvailable reports whether the given player alias resolves to an
// executable this machine can actually run. The frontend uses it to grey
// out players that are not installed instead of failing at click time.
func PlayerAvailable(name string) bool {
	if name == "" || strings.EqualFold(name, "mpv") {
		_, err := exec.LookPath("mpv")
		return err == nil
	}
	resolved := resolvePlayerPath(name)
	if _, err := os.Stat(resolved); err == nil {
		return true
	}
	_, err := exec.LookPath(resolved)
	return err == nil
}

// fallbackReferer is the origin each source's CDN expects when the scraper
// did not record one. mpv receives the real headers through the scraper's
// metadata; external players only get what we put on the command line.
func fallbackReferer(sourceName string) string {
	switch {
	case strings.EqualFold(sourceName, string(source.AllAnime)):
		return "https://allmanga.to"
	case strings.HasPrefix(strings.ToLower(sourceName), "animefire"):
		return "https://animefire.io"
	case strings.EqualFold(sourceName, string(source.Goyabu)):
		return "https://goyabu.io"
	case strings.EqualFold(sourceName, string(source.SuperFlix)):
		return "https://superflixapi.link"
	}
	return ""
}

// refererForSource prefers the referer the scraper actually recorded for
// the stream it just resolved, falling back to the per-source origin.
func refererForSource(sourceName string) string {
	if ref := currentReferer(); ref != "" {
		return ref
	}
	return fallbackReferer(sourceName)
}

// argsForPlayer returns the flags that must precede the stream URL for the
// given player, so streams behind hotlink protection still play.
func argsForPlayer(playerPath, referer string) []string {
	if referer == "" {
		return nil
	}
	base := strings.ToLower(filepath.Base(playerPath))
	switch {
	case strings.Contains(base, "vlc"):
		return []string{"--http-referrer=" + referer, "--no-video-title-show"}
	case strings.Contains(base, "mpv"):
		return []string{"--referrer=" + referer}
	case strings.Contains(base, "mpc"):
		return []string{"/referer", referer}
	case strings.Contains(base, "pot"):
		return []string{"/referer=" + referer}
	}
	return nil
}

// PlayWith launches a player on the given stream URL. An empty playerPath
// (or "mpv") routes through the CLI's mpv IPC launcher, which carries the
// full header set. Any other player is spawned with a referer flag when it
// supports one. Returns a descriptive error if the player fails to start or
// exits within the grace period.
func PlayWith(streamURL, playerPath, sourceName string) error {
	return playWithReferer(streamURL, playerPath, sourceName, refererForSource(sourceName))
}

// playWithReferer is PlayWith with the referer supplied explicitly. Callers
// that just resolved a stream pass the referer they captured at that moment,
// because util.GetGlobalReferer is process-wide and a concurrent download
// may have moved it on by now.
func playWithReferer(streamURL, playerPath, sourceName, referer string) error {
	if strings.TrimSpace(streamURL) == "" {
		return fmt.Errorf("link do vídeo vazio")
	}
	if referer == "" {
		referer = fallbackReferer(sourceName)
	}

	name := strings.ToLower(strings.TrimSpace(playerPath))
	if name == "" || name == "mpv" {
		if _, err := player.StartVideo(streamURL, nil); err != nil {
			return fmt.Errorf("não foi possível iniciar o mpv: %w", err)
		}
		return nil
	}

	realPath := resolvePlayerPath(playerPath)
	args := argsForPlayer(realPath, referer)
	args = append(args, streamURL)

	// #nosec G204: playerPath came from a native file picker or one of the
	// predefined aliases above, both treated as trusted input.
	cmd := exec.Command(realPath, args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("não foi possível abrir %q: %w", realPath, err)
	}

	// Wait briefly so immediate failures surface in the GUI instead of
	// looking like a successful launch with no window.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("o %s fechou: %v (detalhe: %s)",
				filepath.Base(realPath), err, strings.TrimSpace(stderr.String()))
		}
		return fmt.Errorf("o %s fechou na hora — este vídeo provavelmente exige cabeçalhos que esse player não envia",
			filepath.Base(realPath))
	case <-time.After(playerStartGrace):
		// Still running: detach the Wait goroutine and report success.
		go func() { <-done }()
		return nil
	}
}

// PlayEpisode resolves an episode at the requested quality and launches it
// with the chosen player in one call. An empty playerPath means mpv; an
// empty quality means whatever the session default is.
func PlayEpisode(r SearchResult, ep EpisodeResult, playerPath, quality string) error {
	url, referer, err := resolveWithQuality(r, ep, quality)
	if err != nil {
		return err
	}
	if err := playWithReferer(url, playerPath, r.Source, referer); err != nil {
		return err
	}

	// Only record a watch once the player actually started — a failed
	// launch should not pollute the history.
	if recErr := RecordWatch(r, ep); recErr != nil {
		util.Debug("could not record watch history", "error", recErr)
	}
	return nil
}
