package guiapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alvarorichard/Goanime/internal/api"
	"github.com/alvarorichard/Goanime/internal/downloader/hls"
	"github.com/alvarorichard/Goanime/internal/player"
	"github.com/alvarorichard/Goanime/internal/util"
)

// Download job states, as sent to the frontend.
const (
	StateResolving   = "resolving"
	StateDownloading = "downloading"
	StateDone        = "done"
	StateError       = "error"
	StateCancelled   = "cancelled"
)

// downloadTimeout bounds a single episode download. Movies from SuperFlix
// can run past two hours of video, so this is deliberately generous.
const downloadTimeout = 90 * time.Minute

// progressThrottle is the minimum gap between progress notifications for a
// job. HLS fires a callback per segment, which would otherwise flood the
// webview bridge with thousands of messages.
const progressThrottle = 250 * time.Millisecond

// DownloadProgress is one job's state as the frontend sees it.
type DownloadProgress struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	State    string  `json:"state"`
	Percent  float64 `json:"percent"`
	Received int64   `json:"received"`
	Total    int64   `json:"total"`
	Path     string  `json:"path"`
	Error    string  `json:"error"`
}

// job is the internal bookkeeping behind a DownloadProgress.
type job struct {
	progress DownloadProgress
	cancel   context.CancelFunc
	lastSent time.Time
}

var (
	jobsMu sync.Mutex
	jobs   = map[string]*job{}
	jobSeq int

	// progressHook is set by the Wails layer so job updates can be pushed to
	// the frontend as events. guiapi stays free of any Wails import.
	hookMu       sync.RWMutex
	progressHook func(DownloadProgress)
)

// SetProgressHook registers the callback invoked on every job state change.
// Passing nil clears it. The hook may be called from any goroutine.
func SetProgressHook(fn func(DownloadProgress)) {
	hookMu.Lock()
	progressHook = fn
	hookMu.Unlock()
}

// emit pushes a snapshot to the hook, if one is registered.
func emit(p DownloadProgress) {
	hookMu.RLock()
	fn := progressHook
	hookMu.RUnlock()
	if fn != nil {
		fn(p)
	}
}

// QualityOption is one entry of the quality picker.
type QualityOption struct {
	// Value is what gets passed back to the play/download calls. It matches
	// the CLI's --quality vocabulary.
	Value string `json:"value"`
	Label string `json:"label"`
}

// Qualities returns the quality preferences the GUI offers. The sources do
// not expose a uniform per-episode ladder, so this mirrors the CLI's
// --quality flag: a preference that each scraper resolves to the nearest
// stream it actually has.
func Qualities() []QualityOption {
	return []QualityOption{
		{Value: "best", Label: "Melhor disponível"},
		{Value: "1080p", Label: "1080p"},
		{Value: "720p", Label: "720p"},
		{Value: "480p", Label: "480p"},
		{Value: "360p", Label: "360p"},
		{Value: "worst", Label: "Menor arquivo"},
	}
}

// qualityMu serialises access to util.GlobalQuality, which the scrapers read
// as process-wide state. The GUI can have a play and a download in flight at
// once, so the value is set and restored around each resolution rather than
// left dangling for whatever runs next.
var qualityMu sync.Mutex

// resolveWithQuality resolves a stream under a specific quality preference
// and returns the URL together with the referer the scraper recorded for it.
func resolveWithQuality(r SearchResult, ep EpisodeResult, quality string) (streamURL, referer string, err error) {
	qualityMu.Lock()
	defer qualityMu.Unlock()

	previous := util.GlobalQuality
	if quality != "" {
		util.GlobalQuality = quality
	}
	defer func() { util.GlobalQuality = previous }()

	streamURL, err = ResolveStreamURL(r, ep)
	if err != nil {
		return "", "", err
	}
	return streamURL, util.GetGlobalReferer(), nil
}

// DownloadStatus returns a snapshot of every job, newest first. The frontend
// calls it once on boot; live updates arrive through the progress hook.
func DownloadStatus() []DownloadProgress {
	jobsMu.Lock()
	defer jobsMu.Unlock()

	out := make([]DownloadProgress, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.progress)
	}
	// Job IDs are monotonic, so a numeric descending sort is newest-first.
	for i := range out {
		for k := i + 1; k < len(out); k++ {
			if jobNum(out[k].ID) > jobNum(out[i].ID) {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	return out
}

func jobNum(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "job-"))
	return n
}

// CancelDownload stops a running job. It reports whether a job with that ID
// was actually running.
func CancelDownload(id string) bool {
	jobsMu.Lock()
	j, ok := jobs[id]
	jobsMu.Unlock()
	if !ok || j.cancel == nil {
		return false
	}
	j.cancel()
	return true
}

// ClearFinishedDownloads drops completed, failed and cancelled jobs from the
// list so the panel does not grow without bound.
func ClearFinishedDownloads() {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	for id, j := range jobs {
		switch j.progress.State {
		case StateDone, StateError, StateCancelled:
			delete(jobs, id)
		}
	}
}

// update mutates a job's progress under the lock and pushes it to the hook.
// force bypasses the throttle; use it for state transitions, which must
// never be dropped.
func update(id string, force bool, mutate func(*DownloadProgress)) {
	jobsMu.Lock()
	j, ok := jobs[id]
	if !ok {
		jobsMu.Unlock()
		return
	}
	mutate(&j.progress)
	if !force && time.Since(j.lastSent) < progressThrottle {
		jobsMu.Unlock()
		return
	}
	j.lastSent = time.Now()
	snapshot := j.progress
	jobsMu.Unlock()

	emit(snapshot)
}

// StartDownload begins downloading an episode in the background and returns
// the job ID immediately. Progress arrives through the hook registered with
// SetProgressHook.
func StartDownload(r SearchResult, ep EpisodeResult, quality string) (string, error) {
	if strings.TrimSpace(r.Name) == "" {
		return "", fmt.Errorf("nenhum título selecionado")
	}

	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)

	jobsMu.Lock()
	jobSeq++
	id := fmt.Sprintf("job-%d", jobSeq)
	label := downloadLabel(r, ep)
	jobs[id] = &job{
		progress: DownloadProgress{ID: id, Title: label, State: StateResolving},
		cancel:   cancel,
	}
	snapshot := jobs[id].progress
	jobsMu.Unlock()

	emit(snapshot)

	go func() {
		defer cancel()
		if err := runDownload(ctx, id, r, ep, quality); err != nil {
			// A cancelled context is the user's own doing, not a failure.
			if ctx.Err() != nil {
				update(id, true, func(p *DownloadProgress) {
					p.State = StateCancelled
				})
				return
			}
			update(id, true, func(p *DownloadProgress) {
				p.State = StateError
				p.Error = err.Error()
			})
		}
	}()

	return id, nil
}

// downloadLabel is the human-readable name shown in the downloads panel.
func downloadLabel(r SearchResult, ep EpisodeResult) string {
	name := util.SanitizeForFilename(r.Name)
	if ep.SeasonID != "" {
		return fmt.Sprintf("%s — T%sE%s", name, ep.SeasonID, ep.Number)
	}
	return fmt.Sprintf("%s — Ep. %s", name, ep.Number)
}

// runDownload resolves the stream then writes it to disk, choosing between
// the HLS segment downloader and a plain streaming GET.
func runDownload(ctx context.Context, id string, r SearchResult, ep EpisodeResult, quality string) error {
	streamURL, referer, err := resolveWithQuality(r, ep, quality)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	destPath, err := downloadPath(r, ep)
	if err != nil {
		return err
	}

	// Never silently clobber an existing file: report it as done instead.
	if info, statErr := os.Stat(destPath); statErr == nil && info.Size() > 0 {
		update(id, true, func(p *DownloadProgress) {
			p.State = StateDone
			p.Percent = 100
			p.Path = destPath
			p.Total = info.Size()
			p.Received = info.Size()
		})
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return fmt.Errorf("não foi possível criar a pasta de destino: %w", err)
	}

	update(id, true, func(p *DownloadProgress) {
		p.State = StateDownloading
		p.Path = destPath
	})

	headers := map[string]string{}
	if referer != "" {
		headers["Referer"] = referer
	}

	if player.LooksLikeHLS(streamURL) {
		err = downloadHLS(ctx, id, streamURL, destPath, headers)
	} else {
		err = downloadDirect(ctx, id, streamURL, destPath, headers)
	}
	if err != nil {
		// Leave no half-written file behind for the next run to mistake
		// for a completed download.
		_ = os.Remove(destPath)
		return err
	}

	final, _ := os.Stat(destPath)
	update(id, true, func(p *DownloadProgress) {
		p.State = StateDone
		p.Percent = 100
		if final != nil {
			p.Total = final.Size()
			p.Received = final.Size()
		}
	})
	return nil
}

// downloadHLS drives the segment downloader, reporting progress by segment
// count because an HLS playlist has no overall content length.
func downloadHLS(ctx context.Context, id, streamURL, destPath string, headers map[string]string) error {
	cb := func(bytesWritten int64, segmentsWritten, totalSegments int) {
		update(id, false, func(p *DownloadProgress) {
			p.Received = bytesWritten
			if totalSegments > 0 {
				p.Percent = float64(segmentsWritten) / float64(totalSegments) * 100
			}
		})
	}
	if err := hls.DownloadToFile(ctx, streamURL, destPath, headers, cb); err != nil {
		return fmt.Errorf("falha ao baixar o vídeo (HLS): %w", err)
	}
	return nil
}

// downloadDirect streams a regular file to disk, reporting byte progress.
func downloadDirect(ctx context.Context, id, streamURL, destPath string, headers map[string]string) error {
	req, err := newDownloadRequest(ctx, streamURL, headers)
	if err != nil {
		return err
	}

	resp, err := util.GetDownloadClient().Do(req)
	if err != nil {
		return fmt.Errorf("não foi possível acessar o vídeo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("o servidor do vídeo respondeu HTTP %d", resp.StatusCode)
	}

	total := resp.ContentLength
	update(id, true, func(p *DownloadProgress) { p.Total = total })

	// #nosec G304: destPath is built from sanitized components by downloadPath.
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("não foi possível criar o arquivo de destino: %w", err)
	}
	defer func() { _ = f.Close() }()

	written, err := copyWithProgress(ctx, f, resp.Body, id, total)
	if err != nil {
		return err
	}
	if total > 0 && written < total {
		return fmt.Errorf("o download terminou antes da hora: %d de %d bytes", written, total)
	}
	return nil
}

// newDownloadRequest builds the GET for a direct file download. The URL is
// validated against the same SSRF guard the rest of the codebase uses before
// any request goes out.
func newDownloadRequest(ctx context.Context, streamURL string, headers map[string]string) (*http.Request, error) {
	if err := api.ValidateExternalURL(streamURL); err != nil {
		return nil, fmt.Errorf("download recusado por segurança a partir de %q: %w", streamURL, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, fmt.Errorf("link do vídeo inválido: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// copyWithProgress is io.Copy with cancellation checks and progress updates.
func copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, id string, total int64) (int64, error) {
	buf := make([]byte, 256*1024)
	var written int64

	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return written, fmt.Errorf("falha ao gravar: %w", writeErr)
			}
			written += int64(n)
			update(id, false, func(p *DownloadProgress) {
				p.Received = written
				if total > 0 {
					p.Percent = float64(written) / float64(total) * 100
				}
			})
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, fmt.Errorf("falha ao ler: %w", readErr)
		}
	}
}

// downloadPath builds the destination using the same Plex-style layout the
// CLI writes to, so both entrypoints fill one library.
func downloadPath(r SearchResult, ep EpisodeResult) (string, error) {
	name := util.SanitizeForFilename(r.Name)
	if name == "" {
		return "", fmt.Errorf("não foi possível montar um nome de arquivo para %q", r.Name)
	}

	isMovieOrTV := r.MediaType == "movie" || r.MediaType == "tv"

	baseDir := util.GlobalOutputDir
	if baseDir == "" {
		if isMovieOrTV {
			baseDir = util.DefaultMovieDownloadDir()
		} else {
			baseDir = util.DefaultDownloadDir()
		}
	}

	// A movie is a single file, with no season folder around it.
	if r.MediaType == "movie" {
		return filepath.Join(baseDir, util.BuildMediaFileName(name, nil)+".mp4"), nil
	}

	season := 1
	if n, err := strconv.Atoi(ep.SeasonID); err == nil && n > 0 {
		season = n
	}
	epNum := ep.Num
	if epNum == 0 {
		if n, err := strconv.Atoi(ep.Number); err == nil {
			epNum = n
		}
	}

	dir := util.FormatPlexEpisodeDir(baseDir, name, season)
	return filepath.Join(dir, util.PlexEpisodeFilename(name, season, epNum)), nil
}

// DownloadsFolder returns the directory downloads land in, so the GUI can
// offer to open it.
func DownloadsFolder() string {
	if util.GlobalOutputDir != "" {
		return util.GlobalOutputDir
	}
	return util.DefaultDownloadDir()
}
