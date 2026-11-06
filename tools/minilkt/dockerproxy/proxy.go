// Package dockerproxy is a caching man-in-the-middle HTTP(S) proxy for
// container image builds. Every CONNECT is intercepted with a private CA and
// every response is stored under the base directory, so a build that ran
// once can be repeated from the cache.
//
// New lays out the base directory as
//
//	<baseDir>/certs/cert.pem   CA certificate, for SSL_CERT_FILE or a bind mount into build steps
//	<baseDir>/certs/ca.crt     the same certificate, named for certs.d-style scanners such as DockerCertPath
//	<baseDir>/certs/key.pem    CA private key
//	<baseDir>/cache/           one directory per cached request
//
// Clients reach the proxy through HTTP_PROXY and HTTPS_PROXY pointing at
// localhost:<port>, on either loopback address, and have to trust the CA.
package dockerproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/elazarl/goproxy"
)

const (
	certsSubdir = "certs"
	cacheSubdir = "cache"
	stopTimeout = 5 * time.Second
)

// defaultExcludedHosts are always served live: the Docker Hub token service
// hands out short-lived bearer tokens that must stay fresh.
var defaultExcludedHosts = []string{"auth.docker.io"}

// Proxy is one proxy instance with all of its state under Dir.
type Proxy struct {
	Dir      string // base directory, absolute
	CertsDir string // Dir/certs, usable as the DockerCertPath of a containers/image SystemContext
	CertPEM  string // CertsDir/cert.pem, the CA certificate
	CACrt    string // CertsDir/ca.crt, the same certificate under the name certs.d scanners pick up
	CacheDir string // Dir/cache

	// Verbose logs one line per cache hit and miss. Debug turns on goproxy's
	// own per-request output. Both go to the standard logger; set them
	// before Start.
	Verbose bool
	Debug   bool

	ca        *tls.Certificate
	cache     *cache
	transport *http.Transport
	excluded  map[string]struct{} // hosts served live, never from the cache

	mu     sync.Mutex
	srv    *http.Server
	served chan struct{}
}

// New prepares baseDir, creating it and its certs and cache subdirectories
// as needed, and loads the CA from certs, generating one on first use.
func New(baseDir string) (*Proxy, error) {
	dir, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}
	p := &Proxy{
		Dir:      dir,
		CertsDir: filepath.Join(dir, certsSubdir),
		CacheDir: filepath.Join(dir, cacheSubdir),
		// Upstream connections go out directly. The process may well point
		// HTTPS_PROXY at this proxy, and following that would loop.
		transport: &http.Transport{Proxy: nil},
		excluded:  map[string]struct{}{},
	}
	p.CertPEM = filepath.Join(p.CertsDir, certFile)
	p.CACrt = filepath.Join(p.CertsDir, caFile)
	for _, d := range []string{p.CertsDir, p.CacheDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if p.ca, err = loadOrCreateCA(p.CertsDir); err != nil {
		return nil, err
	}
	p.cache = &cache{dir: p.CacheDir}
	for _, h := range defaultExcludedHosts {
		p.excluded[h] = struct{}{}
	}
	return p, nil
}

func (p *Proxy) WipeCache() error {
	return os.RemoveAll(p.CacheDir)
}

// Start serves the proxy on port of both loopback addresses until Stop is
// called, so a proxy URL naming localhost works whichever address a client
// resolves it to.
func (p *Proxy) Start(port uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.srv != nil {
		return errors.New("dockerproxy: already started")
	}
	listeners, err := listenLoopback(port)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: p.handler()}
	served := make(chan struct{})
	var serving sync.WaitGroup
	var addrs []string
	for _, ln := range listeners {
		addrs = append(addrs, ln.Addr().String())
		serving.Add(1)
		go func() {
			defer serving.Done()
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("dockerproxy: %v", err)
			}
		}()
	}
	go func() {
		serving.Wait()
		close(served)
	}()
	p.srv, p.served = srv, served
	log.Printf("dockerproxy: listening on %s, cache in %s", strings.Join(addrs, " and "), p.CacheDir)
	return nil
}

// listenLoopback binds port on 127.0.0.1 and, where the host has IPv6, on
// ::1. Build containers resolve localhost to ::1 first and pasta forwards
// that to the host's ::1, so IPv4 alone leaves them with a reset connection.
func listenLoopback(port uint16) ([]net.Listener, error) {
	v4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	if err != nil {
		return nil, err
	}
	actual := strconv.Itoa(v4.Addr().(*net.TCPAddr).Port)
	v6, err := net.Listen("tcp6", net.JoinHostPort("::1", actual))
	if err != nil {
		if errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EAFNOSUPPORT) {
			return []net.Listener{v4}, nil
		}
		v4.Close()
		return nil, err
	}
	return []net.Listener{v4, v6}, nil
}

// Stop closes the listener and waits for the server to wind down. CONNECT
// tunnels are hijacked connections and end when their client goes away.
// Stopping a proxy that is not running does nothing.
func (p *Proxy) Stop() error {
	p.mu.Lock()
	srv, served := p.srv, p.served
	p.srv, p.served = nil, nil
	p.mu.Unlock()
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	err := srv.Shutdown(ctx)
	if err != nil {
		srv.Close()
	}
	<-served
	p.transport.CloseIdleConnections()
	return err
}

func (p *Proxy) handler() http.Handler {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr = p.transport
	proxy.CertStore = newCertStore()
	proxy.AllowHTTP2 = false
	proxy.Logger = log.Default()
	proxy.Verbose = p.Debug
	proxy.PreventCanonicalization = true
	proxy.KeepDestinationHeaders = true
	proxy.KeepAcceptEncoding = true
	proxy.KeepHeader = true

	// Every CONNECT is intercepted, excluded hosts included, so a client
	// whose only trust anchor is cert.pem keeps working for them as well.
	mitm := &goproxy.ConnectAction{Action: goproxy.ConnectMitm, TLSConfig: goproxy.TLSConfigFromCA(p.ca)}
	proxy.OnRequest().HandleConnect(goproxy.FuncHttpsHandler(func(host string, _ *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		return mitm, host
	}))
	proxy.OnRequest().DoFunc(p.onRequest)
	proxy.OnResponse().DoFunc(p.onResponse)
	return proxy
}

// reqState travels from the request to the response handler in ctx.UserData.
type reqState struct {
	fromCache bool
	entry     string // cache entry, settled before the request went upstream
}

func (p *Proxy) onRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	if p.isExcluded(req) {
		return req, nil
	}
	req = req.WithContext(context.WithoutCancel(req.Context())) // needed for busybox wget

	// if req.Method != http.MethodGet && req.Method != http.MethodHead {
	// 	p.logf("cache ignore  %s %s", req.Method, req.URL)
	// 	return req, nil
	// }
	// path consumes the body once, here. After RoundTrip the body belongs to
	// the transport, which closes it on its own goroutine, so the entry is
	// carried over instead of being computed again from resp.Request.
	entry := p.cache.path(req)
	resp, err := p.cache.open(entry, req)
	if err != nil {
		log.Printf("dockerproxy: cache lookup for %s %s: %v", req.Method, req.URL, err)
		return req, nil
	}
	if resp != nil {
		p.logf("cache hit  %s %s", req.Method, req.URL)
		ctx.UserData = &reqState{fromCache: true}
		return req, resp
	}
	p.logf("cache miss %s %s", req.Method, req.URL)
	ctx.UserData = &reqState{entry: entry}
	return req, nil
}

// logf writes a line when Verbose is set.
func (p *Proxy) logf(format string, args ...any) {
	if p.Verbose {
		log.Printf("dockerproxy: "+format, args...)
	}
}

// onResponse stores a live response and answers with the stored copy, so the
// client sees exactly what later cache hits will see.
func (p *Proxy) onResponse(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
	st, ok := ctx.UserData.(*reqState)
	if resp == nil || resp.Request == nil || !ok || st.fromCache {
		return resp
	}
	if err := p.cache.store(st.entry, resp); err != nil {
		return p.failed(resp.Request, "store", err)
	}
	cached, err := p.cache.open(st.entry, resp.Request)
	if err == nil && cached == nil {
		err = errors.New("entry missing right after store")
	}
	if err != nil {
		return p.failed(resp.Request, "reopen", err)
	}
	return cached
}

func (p *Proxy) failed(req *http.Request, what string, err error) *http.Response {
	log.Printf("dockerproxy: cache %s for %s %s: %v", what, req.Method, req.URL, err)
	return goproxy.NewResponse(req, "text/plain", http.StatusBadGateway, "dockerproxy: cache "+what+" failed: "+err.Error())
}

func (p *Proxy) isExcluded(req *http.Request) bool {
	host := req.URL.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	_, excluded := p.excluded[host]
	return excluded
}
