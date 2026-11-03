package dockerproxy

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

type tmpFileReadCloser struct {
	fh *os.File
}

func newTmpFileReadCloser(i io.ReadCloser) (*tmpFileReadCloser, string) {
	fh, err := os.CreateTemp("", "tmpFileReadCloser-*")
	if err != nil {
		panic(err)
	}

	_, err = io.Copy(fh, i)
	if err != nil {
		panic(err)
	}

	err = i.Close()
	if err != nil {
		panic(err)
	}

	_, err = fh.Seek(0, 0)
	if err != nil {
		panic(err)
	}

	h := md5.New()
	_, err = io.Copy(h, fh)
	if err != nil {
		panic(err)
	}

	_, err = fh.Seek(0, 0)
	if err != nil {
		panic(err)
	}

	return &tmpFileReadCloser{fh: fh}, hex.EncodeToString(h.Sum([]byte{}))
}

func (t *tmpFileReadCloser) Read(bs []byte) (int, error) {
	return t.fh.Read(bs)
}

func (t *tmpFileReadCloser) Close() error {
	if t.fh == nil {
		return nil
	}
	errClose := t.fh.Close()

	errRemove := os.Remove(t.fh.Name())

	t.fh = nil

	return errors.Join(errClose, errRemove)
}
