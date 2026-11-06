package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"archive/tar"

	"go.podman.io/storage/pkg/archive"

	"github.com/opencontainers/go-digest"
	imgspecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"go.podman.io/image/v5/manifest"
	"go.podman.io/storage"
	"go.podman.io/storage/pkg/unshare"
)

// cacheImageName is stored the way podman stores a locally built image, so
// c/image resolves it by name and the listing has a tag to show.
const cacheImageName = "localhost/cache-export:latest"

func importCache(cacheImageID, dir string) error {
	unshare.MaybeReexecUsingUserNamespace(false)

	store, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _, _ = store.Shutdown(false) }()

	img, err := store.Image(cacheImageID)
	if err != nil {
		return err
	}

	uncompressed := archive.Uncompressed
	r, err := store.Diff("", img.TopLayer, &storage.DiffOptions{
		Compression: &uncompressed,
	})
	if err != nil {
		return err
	}

	os.MkdirAll(dir, 0o755)
	root, err := os.OpenRoot(dir)
	if err != nil {
		panic(err)
	}
	defer root.Close()

	tr := tar.NewReader(r)
	for {
		hd, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		fmt.Printf("%s\n", hd.Name)
		root.MkdirAll(filepath.Dir(hd.Name), 0o755)
		fh, err := root.Create(hd.Name)
		if err != nil {
			return err
		}
		defer fh.Close()
		_, err = io.CopyN(fh, tr, hd.Size)
		if err != nil {
			return err
		}
	}

	defer r.Close()

	return nil
}

func exportCache(dir, cacheImageName string) (*storage.Image, error) {
	unshare.MaybeReexecUsingUserNamespace(false)

	store, err := openStore()
	if err != nil {
		return nil, err
	}
	defer func() { _, _ = store.Shutdown(false) }()

	if old, err := store.Image(cacheImageName); err == nil {
		if _, err := store.DeleteImage(old.ID, true); err != nil {
			return nil, err
		}
	}

	r, w := io.Pipe()

	wait := make(chan struct{})
	var layer *storage.Layer
	go func() {
		var err error
		layer, _, err = store.PutLayer("", "", nil, "", false, nil, r)
		if err != nil {
			panic(err)
		}

		wait <- struct{}{}
	}()
	tw := tar.NewWriter(w)

	filepath.Walk(dir, func(path string, fi fs.FileInfo, err error) error {
		if !fi.Mode().IsRegular() {
			return nil
		}

		// create a new dir/file header
		header, err := tar.FileInfoHeader(fi, fi.Name())
		if err != nil {
			return err
		}

		// update the name to correctly reflect the desired destination when untaring
		header.Name = strings.TrimPrefix(strings.Replace(path, dir, "", -1), string(filepath.Separator))

		// write the header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// open files for taring
		f, err := os.Open(path)
		if err != nil {
			return err
		}

		// copy file data into tar writer
		if _, err := io.Copy(tw, f); err != nil {
			return err
		}

		// manually close here after each file operation; defering would cause each file close
		// to wait until all operations have completed.
		f.Close()

		return nil
	})

	tw.Close()
	w.Close()

	fmt.Printf("waiting for upload ...\n")
	<-wait

	opts, err := cacheImageOptions(layer)
	if err != nil {
		return nil, err
	}
	img, err := store.CreateImage("", []string{cacheImageName}, layer.ID, "", opts)
	if err != nil {
		panic(err)
	}

	return img, nil
}

// cacheImageOptions builds the OCI config and manifest c/image needs to read
// the image back, stored under the big-data keys its storage destination
// uses on commit.
func cacheImageOptions(layer *storage.Layer) (*storage.ImageOptions, error) {
	created := time.Now()
	config, err := json.Marshal(imgspecv1.Image{
		Created:  &created,
		Platform: imgspecv1.Platform{Architecture: runtime.GOARCH, OS: "linux"},
		RootFS:   imgspecv1.RootFS{Type: "layers", DiffIDs: []digest.Digest{layer.UncompressedDigest}},
	})
	if err != nil {
		return nil, err
	}
	configDigest := digest.FromBytes(config)
	man, err := manifest.OCI1FromComponents(
		imgspecv1.Descriptor{MediaType: imgspecv1.MediaTypeImageConfig, Digest: configDigest, Size: int64(len(config))},
		[]imgspecv1.Descriptor{{MediaType: imgspecv1.MediaTypeImageLayer, Digest: layer.UncompressedDigest, Size: layer.UncompressedSize}},
	).Serialize()
	if err != nil {
		return nil, err
	}
	manifestDigest := digest.FromBytes(man)
	return &storage.ImageOptions{
		CreationDate: created,
		BigData: []storage.ImageBigDataOption{
			{Key: configDigest.String(), Data: config, Digest: configDigest},
			{Key: storage.ImageDigestManifestBigDataNamePrefix + "-" + manifestDigest.String(), Data: man, Digest: manifestDigest},
			{Key: storage.ImageDigestBigDataKey, Data: man, Digest: manifestDigest},
		},
	}, nil
}
