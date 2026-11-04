package dockerproxy

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func request(t *testing.T, method, rawURL string, header http.Header) *http.Request {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if header == nil {
		header = http.Header{}
	}
	return &http.Request{Method: method, URL: u, Header: header}
}

// The paths below pin the on-disk layout so an existing cache directory keeps
// working: the original dockerproxy layout with the request body hash appended
// (empty, hence the trailing "+", for bodyless requests). Quirks such as the
// unpadded hex directory names are part of the contract.
func TestCachePathMatchesOriginalLayout(t *testing.T) {
	c := &cache{dir: "/cache"}
	for _, tc := range []struct {
		method, url string
		header      http.Header
		want        string
	}{
		{
			"GET", "https://registry-1.docker.io/v2/library/alpine/manifests/3.21",
			http.Header{"Accept": {"application/vnd.oci.image.index.v1+json"}, "Authorization": {"Bearer x"}, "User-Agent": {"containers/5.41.0"}},
			"/cache/732a/ddc0/6644/GET+https%3A%2F%2Fregistry-1.docker.io%2Fv2%2Flibrary%2Falpine%2Fmanifests%2F3.21+map%5BAccept%3A%5Bapplication%2Fvnd.oci.im732addc06644a5769316041c7d5958ca",
		},
		{
			"GET", "https://dl-cdn.alpinelinux.org/alpine/v3.21/main/x86_64/APKINDEX.tar.gz", nil,
			"/cache/1811/521/b6b8/GET+https%3A%2F%2Fdl-cdn.alpinelinux.org%2Falpine%2Fv3.21%2Fmain%2Fx86_64%2FAPKINDEX.tar.gz+map%5B%5D+",
		},
		{
			"HEAD", "https://go.dev/dl/go1.25.12.linux-amd64.tar.gz", http.Header{"Range": {"bytes=0-"}},
			"/cache/f4b7/1334/3076/HEAD+https%3A%2F%2Fgo.dev%2Fdl%2Fgo1.25.12.linux-amd64.tar.gz+map%5BRange%3A%5Bbytes%3D0-%5D%5D+",
		},
		{
			"GET", "https://example.com/" + strings.Repeat("a", 120), nil,
			"/cache/2390/8b40/34b9/GET+https%3A%2F%2Fexample.com%2F" + strings.Repeat("a", 76) + "23908b4034b9f624cf62a8b7706f8b19",
		},
	} {
		if got := c.path(request(t, tc.method, tc.url, tc.header)); got != tc.want {
			t.Errorf("path(%s %s)\n got %s\nwant %s", tc.method, tc.url, got, tc.want)
		}
	}
}

func TestCacheKeyIgnoresVolatileHeaders(t *testing.T) {
	c := &cache{dir: "/cache"}
	const u = "https://registry-1.docker.io/v2/library/alpine/blobs/sha256:abc"
	base := c.path(request(t, "GET", u, http.Header{"Accept": {"*/*"}}))
	for name, value := range map[string]string{
		"Authorization": "Bearer secret",
		"User-Agent":    "curl/8",
		"If-None-Match": `"etag"`,
		"Traceparent":   "00-abc-def-01",
	} {
		h := http.Header{"Accept": {"*/*"}, name: {value}}
		if got := c.path(request(t, "GET", u, h)); got != base {
			t.Errorf("header %s changed the cache path", name)
		}
	}
	if got := c.path(request(t, "GET", u, http.Header{"Accept": {"text/html"}})); got == base {
		t.Error("a different Accept header did not change the cache path")
	}
	if got := c.path(request(t, "HEAD", u, http.Header{"Accept": {"*/*"}})); got == base {
		t.Error("a different method did not change the cache path")
	}
}

func TestCacheStoreAndOpenRoundTrip(t *testing.T) {
	c := &cache{dir: t.TempDir()}
	req := request(t, "GET", "https://example.com/file", nil)
	if resp, err := c.open(c.path(req), req); err != nil || resp != nil {
		t.Fatalf("open before store = %v, %v; want nil, nil", resp, err)
	}
	upstream := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": {"text/plain"}, "Transfer-Encoding": {"chunked"}},
		Body:          io.NopCloser(strings.NewReader("payload")),
		ContentLength: -1,
		Request:       req,
	}
	if err := c.store(c.path(req), upstream); err != nil {
		t.Fatal(err)
	}
	resp, err := c.open(c.path(req), req)
	if err != nil || resp == nil {
		t.Fatalf("open after store = %v, %v", resp, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "payload" || resp.ContentLength != 7 {
		t.Fatalf("status %d, body %q, length %d", resp.StatusCode, body, resp.ContentLength)
	}
	if resp.Header.Get("Content-Type") != "text/plain" || resp.Header.Get("Content-Length") != "7" {
		t.Fatalf("headers = %v", resp.Header)
	}
	if _, chunked := resp.Header["Transfer-Encoding"]; chunked || len(resp.TransferEncoding) != 0 {
		t.Fatalf("transfer encoding survived: %v %v", resp.Header, resp.TransferEncoding)
	}
	if resp.Request != req || resp.Proto != "HTTP/1.1" {
		t.Fatalf("request %p (want %p), proto %q", resp.Request, req, resp.Proto)
	}
}

func TestCacheStoreKeepsFirstEntry(t *testing.T) {
	c := &cache{dir: t.TempDir()}
	req := request(t, "GET", "https://example.com/file", nil)
	for _, body := range []string{"first", "second"} {
		resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}
		if err := c.store(c.path(req), resp); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := c.open(c.path(req), req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if body, _ := io.ReadAll(resp.Body); string(body) != "first" {
		t.Fatalf("body = %q, want the first stored response", body)
	}
}

func TestCacheStoreLeavesNoTemporaryDirectories(t *testing.T) {
	dir := t.TempDir()
	c := &cache{dir: dir}
	req := request(t, "GET", "https://example.com/file", nil)
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("x")), Request: req}
	if err := c.store(c.path(req), resp); err != nil {
		t.Fatal(err)
	}
	entry := c.path(req)
	if fi, err := os.Stat(entry); err != nil || !fi.IsDir() {
		t.Fatalf("entry %s: %v", entry, err)
	}
	entries, err := os.ReadDir(filepath.Dir(entry))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly the entry directory, found %d entries", len(entries))
	}
}

func TestCacheOpenHeadUsesStoredContentLength(t *testing.T) {
	c := &cache{dir: t.TempDir()}
	req := request(t, "HEAD", "https://example.com/big", nil)
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Length": {"1234"}},
		Body:       http.NoBody,
		Request:    req,
	}
	if err := c.store(c.path(req), resp); err != nil {
		t.Fatal(err)
	}
	got, err := c.open(c.path(req), req)
	if err != nil || got == nil {
		t.Fatalf("open = %v, %v", got, err)
	}
	defer got.Body.Close()
	body, _ := io.ReadAll(got.Body)
	if len(body) != 0 || got.ContentLength != 1234 || got.Header.Get("Content-Length") != "1234" {
		t.Fatalf("body %q, length %d, header %q", body, got.ContentLength, got.Header.Get("Content-Length"))
	}
}
