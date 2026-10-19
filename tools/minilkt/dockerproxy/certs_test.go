package dockerproxy

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("%s is not a PEM certificate", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestNewCreatesLayoutUnderBaseDir(t *testing.T) {
	base := t.TempDir()
	p, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Dir":      base,
		"CertsDir": filepath.Join(base, "certs"),
		"CertPEM":  filepath.Join(base, "certs", "cert.pem"),
		"CACrt":    filepath.Join(base, "certs", "ca.crt"),
		"CacheDir": filepath.Join(base, "cache"),
	}
	got := map[string]string{"Dir": p.Dir, "CertsDir": p.CertsDir, "CertPEM": p.CertPEM, "CACrt": p.CACrt, "CacheDir": p.CacheDir}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
	for _, dir := range []string{p.CertsDir, p.CacheDir} {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Errorf("%s is not a directory: %v", dir, err)
		}
	}
	certPEM, err := os.ReadFile(p.CertPEM)
	if err != nil {
		t.Fatal(err)
	}
	caCrt, err := os.ReadFile(p.CACrt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(certPEM, caCrt) {
		t.Error("ca.crt and cert.pem differ")
	}
	if fi, err := os.Stat(filepath.Join(p.CertsDir, "key.pem")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("key.pem: %v, mode %v", err, fi.Mode())
	}
	cert := readCert(t, p.CertPEM)
	if !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Errorf("certificate is not usable as a CA: IsCA=%v KeyUsage=%v", cert.IsCA, cert.KeyUsage)
	}
	if cert.NotAfter.Before(time.Now().Add(5 * 365 * 24 * time.Hour)) {
		t.Errorf("CA expires too soon: %v", cert.NotAfter)
	}
	if p.ca == nil || p.ca.Leaf == nil || !p.ca.Leaf.Equal(cert) {
		t.Error("loaded CA does not match the file on disk")
	}
}

func TestNewMakesBaseDirAbsolute(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	p, err := New("proxy")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(p.Dir) || !filepath.IsAbs(p.CertPEM) {
		t.Fatalf("paths are not absolute: %q %q", p.Dir, p.CertPEM)
	}
}

func TestNewReusesExistingCA(t *testing.T) {
	base := t.TempDir()
	first, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	serial := readCert(t, first.CertPEM).SerialNumber
	if err := os.Remove(first.CACrt); err != nil {
		t.Fatal(err)
	}
	second, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := readCert(t, second.CertPEM).SerialNumber; got.Cmp(serial) != 0 {
		t.Fatalf("CA was regenerated: serial %v, want %v", got, serial)
	}
	if got := readCert(t, second.CACrt).SerialNumber; got.Cmp(serial) != 0 {
		t.Fatalf("ca.crt was not rewritten from the existing cert.pem")
	}
}

func TestNewFailsWhenBaseDirIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(file); err == nil {
		t.Fatal("expected an error when the base directory is a file")
	}
}
