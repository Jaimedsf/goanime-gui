package guiapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Artwork used to be fetched straight from the CDN by the webview, which
// left whether it survived a restart entirely up to the webview's own HTTP
// cache — a store this app does not configure, cannot inspect and cannot
// stop from being evicted. metacache.go made the *URLs* free on relaunch;
// this makes the bytes free too, and works with no network at all.
//
// The page asks for /img?u=<encoded url>; this serves it from
// <cache>/goanime/images, fetching once on the first miss.

const (
	// imgPath is the route the frontend uses. Anything else falls through
	// to the embedded assets.
	imgPath = "/img"

	imgFetchTimeout = 20 * time.Second

	// imgClientCacheAge is what we tell the webview. The bytes are already
	// on disk here, so this only saves the round trip through Go.
	imgClientCacheAge = 7 * 24 * time.Hour
)

// The size limits. Vars rather than consts so tests can shrink them: the
// real ceilings are far too large to exercise with a fixture.
var (
	// imgCacheMaxBytes is the ceiling for the whole directory. Covers run
	// 30-90 KB, so this holds several thousand of them.
	imgCacheMaxBytes int64 = 300 << 20

	// imgMaxFileBytes rejects anything too big to be cover art. It guards
	// against a hostile or broken URL filling the disk; it is not a real
	// constraint, as the largest AniList cover is a few hundred KB.
	imgMaxFileBytes int64 = 8 << 20

	// imgSweepEvery is how many bytes of new artwork trigger a size check.
	// Sweeping per write would stat the whole directory on every cover.
	imgSweepEvery int64 = 32 << 20
)

// imgWritten counts bytes cached since the last sweep.
var imgWritten atomic.Int64

// imgSweeping keeps two sweeps from running at once.
var imgSweeping atomic.Bool

// imgDirOverride lets tests use a temp directory.
var imgDirOverride string

// imgDir is where the bytes live: beside the metadata cache, under the OS
// cache dir, because this is regenerable data.
func imgDir() string {
	if imgDirOverride != "" {
		return imgDirOverride
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "goanime", "images")
}

// ImageProxy is the asset-server middleware that serves /img. Its signature
// matches wails assetserver.Middleware; everything it does not recognise is
// passed straight through to the embedded assets.
func ImageProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL == nil || r.URL.Path != imgPath {
			next.ServeHTTP(w, r)
			return
		}
		serveImage(w, r)
	})
}

func serveImage(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("u")
	target, err := parseImageURL(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	path, err := imageCachePath(target.String())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if serveCachedImage(w, r, path) {
		return
	}

	data, err := fetchImage(target)
	if err != nil {
		// The frontend already handles a failed image: setArtwork retries
		// once, then draws a lettered tile. So a failure here is a normal
		// outcome, not something to dress up.
		http.Error(w, "não foi possível buscar a imagem", http.StatusBadGateway)
		return
	}

	// A write that fails is not worth failing the request over: serving the
	// image uncached is still serving the image.
	_ = storeImage(path, data)
	writeImageBytes(w, r, data, filepath.Base(path))
}

// parseImageURL validates the requested URL. Only absolute http(s) URLs are
// proxied: this handler runs inside the app's own origin, so it must not
// become a way to reach anything the page names.
func parseImageURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("faltou o parâmetro u")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("URL inválida")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("apenas http e https")
	}
	if u.Host == "" {
		return nil, errors.New("URL sem host")
	}
	return u, nil
}

// imageKey names the file for a URL. The URL itself cannot be a filename —
// it is too long and full of characters the filesystem reserves.
//
// The result is always 64 hex characters, which is what makes the cache
// path safe: no input, however hostile, can put a separator or a "..'" in
// it. imageCachePath re-checks that rather than trusting this comment.
func imageKey(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])
}

// imageCachePath is the only place a cache filename is built. It proves the
// name is a bare hash before joining it, so path traversal is impossible by
// construction rather than by convention.
func imageCachePath(rawURL string) (string, error) {
	key := imageKey(rawURL)
	if len(key) != 64 {
		return "", fmt.Errorf("chave de cache inesperada (%d caracteres)", len(key))
	}
	for _, c := range key {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", errors.New("chave de cache inesperada")
		}
	}
	return filepath.Join(imgDir(), key), nil
}

// serveCachedImage answers from disk, reporting whether it could.
func serveCachedImage(w http.ResponseWriter, r *http.Request, path string) bool {
	f, err := os.Open(path) // #nosec G304 -- path is a sha256 hex name under our own cache dir
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return false
	}

	// Touch it so eviction can tell what is still in use: the oldest file
	// by modification time is the one nothing has asked for in longest.
	//
	// That touch is also why this must not serve Last-Modified. The mtime
	// changes on every hit, so a conditional request could never match and
	// the webview would re-transfer the whole file each time it revalidated.
	// The ETag below is the honest validator anyway: the bytes for a URL
	// never change, so the key identifies the content exactly.
	now := time.Now()
	_ = os.Chtimes(path, now, now)

	setImageHeaders(w, filepath.Base(path))
	// A zero modtime keeps ServeContent from emitting Last-Modified; it
	// still answers If-None-Match from the ETag. It sniffs the content type
	// from the first bytes, which is what we want: nothing on disk records
	// what the CDN claimed.
	http.ServeContent(w, r, "", time.Time{}, f)
	return true
}

func writeImageBytes(w http.ResponseWriter, r *http.Request, data []byte, etag string) {
	setImageHeaders(w, etag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

// setImageHeaders marks the response cacheable and gives it a validator.
//
// immutable is not a shortcut here, it is the literal truth: the response
// body is whatever that exact URL returned, keyed by the hash of the URL, so
// it can never change for this request. That lets the webview reuse it
// without asking, and get an empty 304 on the rare occasions it does.
func setImageHeaders(w http.ResponseWriter, etag string) {
	w.Header().Set("Cache-Control",
		fmt.Sprintf("private, max-age=%d, immutable", int(imgClientCacheAge.Seconds())))
	if etag != "" {
		w.Header().Set("ETag", `"`+etag+`"`)
	}
}

// imgClient refuses to dial anything that is not a public address. The URLs
// it is handed come from scrapers and from AniList, so they are not fully
// trusted input; the check sits in the dialer rather than on the hostname
// because that is the only place it cannot be sidestepped by DNS.
var imgClient = &http.Client{
	Timeout: imgFetchTimeout,
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
			Control:   denyPrivateAddress,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        16,
	},
}

// nonPublicPrefixes are the ranges the standard library's Is* helpers do not
// cover but that still reach something other than the public internet.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // "this network"
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT, and Tailscale's tailnet
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved, and the broadcast address
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64: embeds an IPv4 address
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"), // 6to4: embeds an IPv4 address
}

func denyPrivateAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("endereço inesperado %q: %w", address, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("endereço inesperado %q", address)
	}
	// An IPv4 address written as ::ffff:a.b.c.d must be judged as the IPv4
	// address it is, or it would slip past every IPv4 range below.
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsMulticast() {
		return fmt.Errorf("endereço não público recusado: %s", ip)
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(ip) {
			return fmt.Errorf("endereço não público recusado: %s", ip)
		}
	}
	return nil
}

// fetchImage downloads one image, refusing anything that is not one.
//
// Fetching a URL the page asked for is the whole job here, so gosec's SSRF
// warning is expected rather than a finding. What makes it safe is not the
// URL check in parseImageURL — a hostname can resolve anywhere — but
// denyPrivateAddress on the dialer, which refuses every non-public address
// at connect time, on redirects too.
func fetchImage(target *url.URL) ([]byte, error) {
	// #nosec G704 -- outbound address restricted by denyPrivateAddress
	req, err := http.NewRequest(http.MethodGet, target.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	// Some CDNs answer 403 to a request with no User-Agent.
	req.Header.Set("User-Agent", "GoAnime-GUI/1.0")
	req.Header.Set("Accept", "image/*")

	// #nosec G704 -- outbound address restricted by denyPrivateAddress
	resp, err := imgClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// One byte past the cap, so a file exactly at the limit still passes
	// and anything larger is detected rather than silently truncated.
	data, err := io.ReadAll(io.LimitReader(resp.Body, imgMaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > imgMaxFileBytes {
		return nil, errors.New("imagem grande demais")
	}
	if len(data) == 0 {
		return nil, errors.New("resposta vazia")
	}
	// Trust the bytes, not the header: this decides what gets written into
	// the app's cache directory.
	if !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return nil, errors.New("a resposta não é uma imagem")
	}
	return data, nil
}

// storeImage writes the file atomically, then schedules a sweep once enough
// new bytes have accumulated to be worth checking.
func storeImage(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".img-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	// Windows will not rename onto an existing file.
	_ = os.Remove(path)
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}

	if imgWritten.Add(int64(len(data))) >= imgSweepEvery {
		imgWritten.Store(0)
		go sweepImageCache()
	}
	return nil
}

// sweepImageCache deletes the least recently served files until the
// directory is back under the ceiling. Modification time is the recency
// signal, kept current by the touch on every cache hit.
func sweepImageCache() {
	if !imgSweeping.CompareAndSwap(false, true) {
		return
	}
	defer imgSweeping.Store(false)

	dir := imgDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	type file struct {
		name string
		size int64
		mod  time.Time
	}
	files := make([]file, 0, len(entries))
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{e.Name(), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= imgCacheMaxBytes {
		return
	}

	// Down to 90% rather than exactly the ceiling, so the next few covers
	// do not each trigger another sweep.
	target := int64(imgCacheMaxBytes) * 9 / 10
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= target {
			return
		}
		if err := os.Remove(filepath.Join(dir, f.name)); err == nil {
			total -= f.size
		}
	}
}

// ImageCacheSize reports how many files and bytes the artwork cache holds.
// It exists so the GUI can show it, and so a test can assert on it without
// reaching into the filesystem layout.
func ImageCacheSize() (files int, total int64) {
	entries, err := os.ReadDir(imgDir())
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files++
		total += info.Size()
	}
	return files, total
}

// ClearImageCache deletes every cached image. The artwork all comes back on
// the next paint, so this is safe to offer as a plain "free up space".
func ClearImageCache() error {
	dir := imgDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var firstErr error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	imgWritten.Store(0)
	return firstErr
}

// imgOnce guards the startup sweep.
var imgOnce sync.Once

// StartImageCacheMaintenance trims the cache once at startup, in the
// background. A session that only ever reads would otherwise never notice
// the directory had grown past the ceiling.
func StartImageCacheMaintenance() {
	imgOnce.Do(func() { go sweepImageCache() })
}
