package dockerproxy

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// cache stores HTTP responses on disk, one directory per request holding the
// files body, headers.json and status. The layout is the one of the original
// dockerproxy tool, so an existing cache directory can be reused.
type cache struct {
	dir string
}

// volatileHeaders take no part in the cache key.
var volatileHeaders = []string{"Traceparent", "If-None-Match", "Authorization", "User-Agent"}

// path returns the entry directory for req. It is byte-identical to the
// original tool, unpadded hex directory names included.
func (c *cache) path(req *http.Request) string {
	headers := req.Header.Clone()
	for _, h := range volatileHeaders {
		headers.Del(h)
	}
	id := fmt.Sprintf("%s %s %+v", req.Method, req.URL, headers)
	sum := md5.Sum([]byte(id))

	dir := c.dir
	for i := 0; i < 6; i += 2 {
		dir = filepath.Join(dir, fmt.Sprintf("%x%x", sum[i], sum[i+1]))
	}
	name := id
	if len(name) > 100 {
		name = name[:100] + hex.EncodeToString(sum[:])
	}
	return filepath.Join(dir, url.QueryEscape(name))
}

// store writes resp to the cache and consumes its body. An existing entry is
// kept. The entry is assembled in a temporary directory and renamed into
// place, so an interrupted store leaves nothing that looks like an entry.
func (c *cache) store(resp *http.Response) error {
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	entry := c.path(resp.Request)
	if _, err := os.Stat(entry); err == nil {
		return nil
	}
	parent := filepath.Dir(entry)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".tmp-")
	if err != nil {
		return err
	}
	if err := writeEntry(tmp, resp); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, entry); err != nil {
		os.RemoveAll(tmp)
		if _, statErr := os.Stat(entry); statErr == nil {
			return nil // a concurrent request stored the same entry first
		}
		return err
	}
	return nil
}

func writeEntry(dir string, resp *http.Response) error {
	body, err := os.Create(filepath.Join(dir, "body"))
	if err != nil {
		return err
	}
	if resp.Body != nil {
		if _, err := io.Copy(body, resp.Body); err != nil {
			body.Close()
			return err
		}
	}
	if err := body.Close(); err != nil {
		return err
	}
	headers, err := json.Marshal(resp.Header)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "headers.json"), headers, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "status"), []byte(strconv.Itoa(resp.StatusCode)), 0o600)
}

// open returns the cached response for req, or nil when there is none. The
// body reads the entry's body file; HEAD requests and empty entries get an
// empty body.
func (c *cache) open(req *http.Request) (*http.Response, error) {
	entry := c.path(req)
	headers, err := os.ReadFile(filepath.Join(entry, "headers.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	resp := &http.Response{
		Request:    req,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}
	if err := json.Unmarshal(headers, &resp.Header); err != nil {
		return nil, fmt.Errorf("%s: %w", entry, err)
	}
	if resp.Header == nil {
		resp.Header = http.Header{}
	}
	status, err := os.ReadFile(filepath.Join(entry, "status"))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode, err = strconv.Atoi(strings.TrimSpace(string(status))); err != nil {
		return nil, fmt.Errorf("%s: %w", entry, err)
	}
	resp.Status = fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))

	body, err := os.Open(filepath.Join(entry, "body"))
	if err != nil {
		return nil, err
	}
	fi, err := body.Stat()
	if err != nil {
		body.Close()
		return nil, err
	}
	resp.ContentLength = fi.Size()
	if resp.ContentLength <= 0 {
		if n, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64); err == nil {
			resp.ContentLength = n
		}
	}
	resp.Header.Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	resp.Header.Del("Transfer-Encoding")
	if resp.ContentLength == 0 || req.Method == http.MethodHead {
		body.Close()
		resp.Body = http.NoBody
	} else {
		resp.Body = body
	}
	return resp, nil
}
