package guiapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// onePixelPNG is the smallest thing http.DetectContentType will call an
// image, so the fixtures exercise the real content sniffing rather than
// working around it.
var onePixelPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
	0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00,
	0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

// useTempImageCache points the artwork cache at a temp directory and lets
// the fetcher reach a local test server.
//
// The real client refuses to dial a private address, which is exactly what
// httptest listens on — so the happy-path tests swap it out. That guard is
// covered on its own by TestDenyPrivateAddress, against the function the
// dialer actually calls.
func useTempImageCache(t *testing.T) {
	t.Helper()

	prevDir, prevClient := imgDirOverride, imgClient
	imgDirOverride = t.TempDir()
	imgClient = &http.Client{}
	imgWritten.Store(0)

	t.Cleanup(func() {
		imgDirOverride, imgClient = prevDir, prevClient
		imgWritten.Store(0)
	})
}

// backdateFile makes file i look older the smaller i is, so eviction has a
// deterministic order to work on instead of whatever the filesystem's clock
// resolution happened to record.
func backdateFile(t *testing.T, path string, i int) {
	t.Helper()
	age := time.Duration(100-i) * time.Minute
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

// imageOrigin is a stand-in CDN that counts how many times it was asked.
func imageOrigin(t *testing.T, body []byte, contentType string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func imageRequest(target string) *http.Request {
	return httptest.NewRequest(http.MethodGet, imgPath+"?u="+url.QueryEscape(target), http.NoBody)
}

// notReached is the asset handler; the proxy must never hand it a /img
// request, and must always hand it everything else.
func notReached(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("the asset handler was reached for %s", r.URL)
	})
}

// The whole point: the CDN is asked once, ever. Everything after that is
// served off the disk, which is what makes a relaunch free.
func TestImageProxyFetchesOriginOnlyOnce(t *testing.T) {
	useTempImageCache(t)
	origin, hits := imageOrigin(t, onePixelPNG, "image/png")

	h := ImageProxy(notReached(t))
	for i := range 3 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, imageRequest(origin.URL+"/cover.png"))

		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status %d", i, rec.Code)
		}
		if got := rec.Body.Bytes(); len(got) != len(onePixelPNG) {
			t.Fatalf("request %d: served %d bytes, want %d", i, len(got), len(onePixelPNG))
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
			t.Errorf("request %d: content type %q", i, ct)
		}
	}

	if n := hits.Load(); n != 1 {
		t.Errorf("the origin was hit %d times, want 1", n)
	}
}

// A cache that does not survive the process is the bug this replaces, so
// prove the bytes are read back from disk and not from memory.
func TestImageProxyServesAFileWrittenByAnEarlierRun(t *testing.T) {
	useTempImageCache(t)

	// No origin at all: if this is served, it came from the file.
	target := "https://cdn.example.test/cover.png"
	path := filepath.Join(imgDirOverride, imageKey(target))
	if err := os.WriteFile(path, onePixelPNG, 0o600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	ImageProxy(notReached(t)).ServeHTTP(rec, imageRequest(target))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if rec.Body.Len() != len(onePixelPNG) {
		t.Errorf("served %d bytes, want %d", rec.Body.Len(), len(onePixelPNG))
	}
}

// Anything that is not the image route belongs to the embedded assets.
func TestImageProxyPassesOtherPathsThrough(t *testing.T) {
	useTempImageCache(t)

	var reached bool
	h := ImageProxy(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/js/main.js", http.NoBody))

	if !reached {
		t.Fatal("the asset handler was not reached")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("status %d — the proxy did not pass the response through", rec.Code)
	}
}

// The handler runs inside the app's own origin, so what it will dereference
// has to be narrow.
func TestImageProxyRejectsURLsItShouldNotFetch(t *testing.T) {
	useTempImageCache(t)
	h := ImageProxy(notReached(t))

	for _, target := range []string{
		"",
		"file:///C:/Windows/win.ini",
		"data:image/png;base64,iVBORw0KGgo=",
		"/etc/passwd",
		"ftp://example.test/cover.png",
		"https://",
	} {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, imageRequest(target))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d for %q, want 400", rec.Code, target)
			}
		})
	}
}

// The response decides what lands in the cache directory, so it is checked
// by its bytes rather than by the header the server chose to send.
func TestImageProxyRefusesAResponseThatIsNotAnImage(t *testing.T) {
	useTempImageCache(t)
	// The content type says image; the body is a script. The bytes win.
	origin, _ := imageOrigin(t, []byte("<html><script>alert(1)</script>"), "image/png")

	rec := httptest.NewRecorder()
	ImageProxy(notReached(t)).ServeHTTP(rec, imageRequest(origin.URL+"/x.png"))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status %d, want 502", rec.Code)
	}
	if files, _ := ImageCacheSize(); files != 0 {
		t.Errorf("%d files cached; a non-image must not be written", files)
	}
}

func TestImageProxyRefusesAnOversizedResponse(t *testing.T) {
	useTempImageCache(t)

	prev := imgMaxFileBytes
	imgMaxFileBytes = int64(len(onePixelPNG)) // one byte short of the body below
	t.Cleanup(func() { imgMaxFileBytes = prev })

	big := append(append([]byte{}, onePixelPNG...), make([]byte, 64)...)
	origin, _ := imageOrigin(t, big, "image/png")

	rec := httptest.NewRecorder()
	ImageProxy(notReached(t)).ServeHTTP(rec, imageRequest(origin.URL+"/huge.png"))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status %d, want 502", rec.Code)
	}
	if files, _ := ImageCacheSize(); files != 0 {
		t.Errorf("%d files cached; an oversized response must not be written", files)
	}
}

// A dead cover URL must not take the page down with it: the frontend draws
// a lettered tile when the image errors, and 502 is what triggers that.
func TestImageProxyAnswers502WhenTheOriginFails(t *testing.T) {
	useTempImageCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	rec := httptest.NewRecorder()
	ImageProxy(notReached(t)).ServeHTTP(rec, imageRequest(srv.URL+"/missing.png"))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status %d, want 502", rec.Code)
	}
}

// The URLs come from scrapers, so they are not trusted input. This is the
// check that keeps the app from being talked into fetching from the machine
// it is running on; it lives in the dialer because a hostname check can be
// walked around with DNS.
func TestDenyPrivateAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		address string
		allow   bool
	}{
		{"93.184.216.34:443", true},
		{"[2606:2800:220:1:248:1893:25c8:1946]:443", true},
		{"127.0.0.1:8080", false},
		{"[::1]:8080", false},
		{"10.0.0.5:80", false},
		{"192.168.1.10:80", false},
		{"172.16.0.1:80", false},
		{"169.254.169.254:80", false}, // the cloud metadata endpoint
		{"0.0.0.0:80", false},
		{"100.101.102.103:80", false}, // a Tailscale peer
		{"198.18.0.1:80", false},
		{"255.255.255.255:80", false},
		{"224.0.0.1:80", false},
		{"[::ffff:127.0.0.1]:80", false}, // IPv4-mapped loopback
		{"[::ffff:10.0.0.5]:80", false},
		{"[fd00::1]:80", false},              // unique local
		{"[64:ff9b::a00:5]:80", false},       // NAT64 of 10.0.0.5
		{"[2002:a00:5::1]:80", false},        // 6to4 of 10.0.0.5
		{"[::ffff:93.184.216.34]:443", true}, // IPv4-mapped public
		{"not-an-address", false},
	}

	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			t.Parallel()
			err := denyPrivateAddress("tcp", tt.address, nil)
			if tt.allow && err != nil {
				t.Errorf("dial refused: %v", err)
			}
			if !tt.allow && err == nil {
				t.Error("dial allowed, want refused")
			}
		})
	}
}

// Two URLs must never collide on one file, and one URL must always land on
// the same file across runs — otherwise the cache either serves the wrong
// art or never hits.
func TestImageKeyIsStableAndDistinct(t *testing.T) {
	t.Parallel()

	a := "https://cdn.example.test/a.png"
	b := "https://cdn.example.test/b.png"

	first, second := imageKey(a), imageKey(a)
	if first != second {
		t.Error("the same URL produced two keys")
	}
	if imageKey(a) == imageKey(b) {
		t.Error("two URLs produced the same key")
	}
	if len(imageKey(a)) != 64 || strings.ContainsAny(imageKey(a), `/\:?*"<>|`) {
		t.Errorf("key %q is not a safe filename", imageKey(a))
	}
}

// The directory must not grow forever, and what survives has to be what is
// still being looked at.
func TestSweepImageCacheDropsTheLeastRecentlyUsed(t *testing.T) {
	useTempImageCache(t)

	prev := imgCacheMaxBytes
	imgCacheMaxBytes = 400
	t.Cleanup(func() { imgCacheMaxBytes = prev })

	// Ten 100-byte files, oldest first by modification time.
	for i := range 10 {
		name := filepath.Join(imgDirOverride, fmt.Sprintf("%064d", i))
		if err := os.WriteFile(name, make([]byte, 100), 0o600); err != nil {
			t.Fatal(err)
		}
		backdateFile(t, name, i)
	}

	sweepImageCache()

	files, bytes := ImageCacheSize()
	if bytes > imgCacheMaxBytes {
		t.Fatalf("%d bytes left, over the %d ceiling", bytes, imgCacheMaxBytes)
	}
	// Down to 90% of the ceiling, so three of ten survive.
	if files != 3 {
		t.Fatalf("%d files left, want 3", files)
	}
	// And they must be the newest three, 7 through 9.
	for i := range 7 {
		if _, err := os.Stat(filepath.Join(imgDirOverride, fmt.Sprintf("%064d", i))); err == nil {
			t.Errorf("file %d survived; it was among the oldest", i)
		}
	}
}

// Under the ceiling, a sweep must leave everything alone.
func TestSweepImageCacheKeepsEverythingUnderTheCeiling(t *testing.T) {
	useTempImageCache(t)

	for i := range 3 {
		name := filepath.Join(imgDirOverride, fmt.Sprintf("%064d", i))
		if err := os.WriteFile(name, make([]byte, 100), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sweepImageCache()

	if files, _ := ImageCacheSize(); files != 3 {
		t.Errorf("%d files left, want all 3", files)
	}
}

func TestClearImageCache(t *testing.T) {
	useTempImageCache(t)

	for i := range 4 {
		name := filepath.Join(imgDirOverride, fmt.Sprintf("%064d", i))
		if err := os.WriteFile(name, onePixelPNG, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if files, bytes := ImageCacheSize(); files != 4 || bytes == 0 {
		t.Fatalf("setup: %d files, %d bytes", files, bytes)
	}

	if err := ClearImageCache(); err != nil {
		t.Fatalf("ClearImageCache: %v", err)
	}
	if files, bytes := ImageCacheSize(); files != 0 || bytes != 0 {
		t.Errorf("%d files and %d bytes left after clearing", files, bytes)
	}

	// A missing directory is nothing to report: there is no cache to clear.
	imgDirOverride = filepath.Join(t.TempDir(), "never-created")
	if err := ClearImageCache(); err != nil {
		t.Errorf("clearing an absent cache: %v", err)
	}
}

// The LRU touch changes the file's mtime on every hit, so Last-Modified
// could never be a usable validator. The ETag has to be the one, or a
// revalidating webview re-transfers the whole image every time.
func TestImageProxyRevalidatesWithAnETag(t *testing.T) {
	useTempImageCache(t)
	origin, hits := imageOrigin(t, onePixelPNG, "image/png")
	target := origin.URL + "/cover.png"

	first := httptest.NewRecorder()
	ImageProxy(notReached(t)).ServeHTTP(first, imageRequest(target))

	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag was served")
	}
	if lm := first.Header().Get("Last-Modified"); lm != "" {
		t.Errorf("Last-Modified = %q; the mtime moves on every hit, so it must not be sent", lm)
	}
	if cc := first.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want it to mark the body immutable", cc)
	}

	// A conditional request must come back empty.
	req := imageRequest(target)
	req.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	ImageProxy(notReached(t)).ServeHTTP(second, req)

	if second.Code != http.StatusNotModified {
		t.Errorf("status %d for a matching If-None-Match, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 carried %d bytes of body", second.Body.Len())
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("the origin was hit %d times, want 1", n)
	}
}

// Two different images must not share a validator, or the webview would
// serve one in place of the other.
func TestImageProxyETagsDifferPerImage(t *testing.T) {
	useTempImageCache(t)
	origin, _ := imageOrigin(t, onePixelPNG, "image/png")

	a := httptest.NewRecorder()
	ImageProxy(notReached(t)).ServeHTTP(a, imageRequest(origin.URL+"/a.png"))
	b := httptest.NewRecorder()
	ImageProxy(notReached(t)).ServeHTTP(b, imageRequest(origin.URL+"/b.png"))

	if a.Header().Get("ETag") == b.Header().Get("ETag") {
		t.Error("two URLs were served the same ETag")
	}
}
