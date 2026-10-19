package dockerproxy

import (
	"crypto/tls"
	"sync"
)

// certStore keeps the per-host certificates goproxy signs in memory, so a
// host is signed once per proxy run.
type certStore struct {
	mu    sync.Mutex
	certs map[string]*tls.Certificate
}

func newCertStore() *certStore {
	return &certStore{certs: map[string]*tls.Certificate{}}
}

// Fetch implements goproxy.CertStorage.
func (s *certStore) Fetch(hostname string, gen func() (*tls.Certificate, error)) (*tls.Certificate, error) {
	s.mu.Lock()
	cert, ok := s.certs[hostname]
	s.mu.Unlock()
	if ok {
		return cert, nil
	}
	cert, err := gen()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if len(s.certs) >= 1024 {
		clear(s.certs)
	}
	s.certs[hostname] = cert
	s.mu.Unlock()
	return cert, nil
}
