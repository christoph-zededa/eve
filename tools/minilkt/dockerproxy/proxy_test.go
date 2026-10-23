package dockerproxy

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// freePort returns a TCP port that was free a moment ago.
func freePort(t *testing.T) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return uint16(ln.Addr().(*net.TCPAddr).Port)
}

// countingServer is an upstream that counts the requests reaching it.
type countingServer struct {
	*httptest.Server
	hits atomic.Int32
}

func newCountingServer(t *testing.T, useTLS bool) *countingServer {
	t.Helper()
	s := &countingServer{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		w.Header().Set("X-Upstream", "yes")
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "hello from %s", r.URL.Path)
	})
	if useTLS {
		s.Server = httptest.NewTLSServer(handler)
	} else {
		s.Server = httptest.NewServer(handler)
	}
	t.Cleanup(s.Close)
	return s
}

// startProxy creates a proxy under a fresh base directory, lets it trust the
// upstream test server, starts it on a free port and returns it with that port.
func startProxy(t *testing.T, upstream *countingServer) (*Proxy, uint16) {
	t.Helper()
	p, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if upstream.TLS != nil {
		pool := x509.NewCertPool()
		pool.AddCert(upstream.Certificate())
		p.transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	}
	port := freePort(t)
	if err := p.Start(port); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Stop() })
	return p, port
}

// proxyClient is an HTTP client that goes through the proxy and trusts only
// the proxy's CA, like a build step with cert.pem mounted as its trust store.
func proxyClient(t *testing.T, p *Proxy, port uint16) *http.Client {
	t.Helper()
	caPEM, err := os.ReadFile(p.CACrt)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("ca.crt holds no certificate")
	}
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	return &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}
}

func get(t *testing.T, client *http.Client, u string) (int, string, http.Header) {
	t.Helper()
	resp, err := client.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body), resp.Header
}

func TestProxyCachesHTTPSResponsesThroughMITM(t *testing.T) {
	// The proxy's own upstream connections must ignore the proxy variables of
	// the process, which minilkt points at this very proxy.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")

	upstream := newCountingServer(t, true)
	p, port := startProxy(t, upstream)
	client := proxyClient(t, p, port)

	for i, wantHits := range []int32{1, 1} {
		status, body, header := get(t, client, upstream.URL+"/file.txt")
		if status != 200 || body != "hello from /file.txt" || header.Get("X-Upstream") != "yes" {
			t.Fatalf("request %d: status %d, body %q, headers %v", i, status, body, header)
		}
		if got := upstream.hits.Load(); got != wantHits {
			t.Fatalf("after request %d upstream hits = %d, want %d", i, got, wantHits)
		}
	}
	if _, _, _ = get(t, client, upstream.URL+"/other.txt"); upstream.hits.Load() != 2 {
		t.Fatal("a different URL did not reach upstream")
	}
}

func TestProxyCachesPlainHTTPResponses(t *testing.T) {
	upstream := newCountingServer(t, false)
	p, port := startProxy(t, upstream)
	client := proxyClient(t, p, port)

	for i, wantHits := range []int32{1, 1} {
		status, body, _ := get(t, client, upstream.URL+"/plain")
		if status != 200 || body != "hello from /plain" {
			t.Fatalf("request %d: status %d, body %q", i, status, body)
		}
		if got := upstream.hits.Load(); got != wantHits {
			t.Fatalf("after request %d upstream hits = %d, want %d", i, got, wantHits)
		}
	}
}

func TestProxyDoesNotCacheExcludedHosts(t *testing.T) {
	upstream := newCountingServer(t, true)
	p, port := startProxy(t, upstream)
	p.excluded["127.0.0.1"] = struct{}{}
	client := proxyClient(t, p, port)

	for i := range 2 {
		if status, _, _ := get(t, client, upstream.URL+"/token"); status != 200 {
			t.Fatalf("request %d: status %d", i, status)
		}
	}
	if got := upstream.hits.Load(); got != 2 {
		t.Fatalf("upstream hits = %d, want 2 for an excluded host", got)
	}
}

func TestProxyHidesCachedBodyFromHead(t *testing.T) {
	upstream := newCountingServer(t, true)
	p, port := startProxy(t, upstream)
	client := proxyClient(t, p, port)

	get(t, client, upstream.URL+"/file.txt")
	resp, err := client.Head(upstream.URL + "/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if body, _ := io.ReadAll(resp.Body); resp.StatusCode != 200 || len(body) != 0 {
		t.Fatalf("HEAD: status %d, body %q", resp.StatusCode, body)
	}
}

func TestStopReleasesPortAndStartWorksAgain(t *testing.T) {
	upstream := newCountingServer(t, false)
	p, port := startProxy(t, upstream)
	client := proxyClient(t, p, port)
	get(t, client, upstream.URL+"/one")

	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(upstream.URL + "/one"); err == nil {
		t.Fatal("proxy still answers after Stop")
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if err := p.Start(port); err != nil {
		t.Fatalf("Start after Stop: %v", err)
	}
	if status, _, _ := get(t, client, upstream.URL+"/one"); status != 200 {
		t.Fatalf("status after restart = %d", status)
	}
	if upstream.hits.Load() != 1 {
		t.Fatal("cache did not survive the restart")
	}
}

// Build containers resolve localhost to ::1 first, and pasta forwards that
// to the host's ::1, so the proxy has to answer on both loopback addresses.
func TestProxyListensOnBothLoopbackAddresses(t *testing.T) {
	if ln, err := net.Listen("tcp6", "[::1]:0"); err != nil {
		t.Skip("no IPv6 loopback on this host")
	} else {
		ln.Close()
	}
	upstream := newCountingServer(t, false)
	p, port := startProxy(t, upstream)
	for _, host := range []string{"127.0.0.1", "[::1]"} {
		client := proxyClient(t, p, port)
		client.Transport.(*http.Transport).Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: fmt.Sprintf("%s:%d", host, port)})
		if status, body, _ := get(t, client, upstream.URL+"/both"); status != 200 || body != "hello from /both" {
			t.Fatalf("via %s: status %d, body %q", host, status, body)
		}
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{fmt.Sprintf("127.0.0.1:%d", port), fmt.Sprintf("[::1]:%d", port)} {
		if conn, err := net.Dial("tcp", addr); err == nil {
			conn.Close()
			t.Fatalf("%s still accepts connections after Stop", addr)
		}
	}
}

// captureLog routes the standard logger into a buffer for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

// startConfigured is startProxy with a hook to set fields before Start.
func startConfigured(t *testing.T, upstream *countingServer, configure func(*Proxy)) (*Proxy, uint16) {
	t.Helper()
	p, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configure(p)
	port := freePort(t)
	if err := p.Start(port); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Stop() })
	return p, port
}

func TestVerboseLogsCacheHitsAndMisses(t *testing.T) {
	out := captureLog(t)
	upstream := newCountingServer(t, false)
	p, port := startConfigured(t, upstream, func(p *Proxy) { p.Verbose = true })
	client := proxyClient(t, p, port)
	get(t, client, upstream.URL+"/v")
	get(t, client, upstream.URL+"/v")
	for _, want := range []string{
		"dockerproxy: cache miss GET " + upstream.URL + "/v",
		"dockerproxy: cache hit  GET " + upstream.URL + "/v",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "INFO: ") {
		t.Errorf("Verbose alone produced goproxy output:\n%s", out.String())
	}
}

func TestDebugLogsGoproxyOutput(t *testing.T) {
	out := captureLog(t)
	upstream := newCountingServer(t, false)
	p, port := startConfigured(t, upstream, func(p *Proxy) { p.Debug = true })
	client := proxyClient(t, p, port)
	get(t, client, upstream.URL+"/d")
	if !strings.Contains(out.String(), "INFO: ") {
		t.Errorf("Debug produced no goproxy output:\n%s", out.String())
	}
	if strings.Contains(out.String(), "dockerproxy: cache miss") {
		t.Errorf("Debug alone logged cache lookups:\n%s", out.String())
	}
}

func TestQuietUnlessVerbose(t *testing.T) {
	out := captureLog(t)
	upstream := newCountingServer(t, false)
	p, port := startProxy(t, upstream)
	client := proxyClient(t, p, port)
	get(t, client, upstream.URL+"/q")
	get(t, client, upstream.URL+"/q")
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !strings.Contains(line, "listening on") {
			t.Errorf("unexpected log line without Verbose: %q", line)
		}
	}
}

func TestStartTwiceFails(t *testing.T) {
	upstream := newCountingServer(t, false)
	p, port := startProxy(t, upstream)
	if err := p.Start(port); err == nil {
		t.Fatal("expected an error from a second Start")
	}
}

func TestStartOnBusyPortFails(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	p, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Start(uint16(ln.Addr().(*net.TCPAddr).Port)); err == nil {
		p.Stop()
		t.Fatal("expected an error for a busy port")
	}
}
