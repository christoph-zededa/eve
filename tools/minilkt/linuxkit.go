package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/opencontainers/go-digest"
	imgspecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	imgcopy "go.podman.io/image/v5/copy"
	"go.podman.io/image/v5/manifest"
	ocilayout "go.podman.io/image/v5/oci/layout"
	"go.podman.io/image/v5/signature"
	istorage "go.podman.io/image/v5/storage"
	"go.podman.io/image/v5/types"
	"go.podman.io/storage"
	"go.podman.io/storage/pkg/unshare"
)

// linuxkitCacheDir mirrors linuxkit's default: $LINUXKIT_CACHE or ~/.linuxkit/cache.
func linuxkitCacheDir() string {
	if dir := os.Getenv("LINUXKIT_CACHE"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	lktCacheDir := filepath.Join(home, ".linuxkit", "cache")
	err := os.MkdirAll(lktCacheDir, 0o700)
	if err != nil {
		panic(err)
	}
	return lktCacheDir
}

// exportToLinuxkitCache copies image from the build store into the linuxkit
// cache, an OCI layout dir, the way linuxkit pkg build leaves a package there:
// the image as lktImage-<arch>, and lktImage as an index naming the platform
// of each of its images. linuxkit hands a plain image stored under lktImage
// out for every architecture; only index entries are matched against the
// platform it builds for. The ref.name annotation c/image writes is the one
// linuxkit looks images up by; io.containerd.image.name is only read by
// `linuxkit cache import`, which this bypasses.
func exportToLinuxkitCache(ctx context.Context, store storage.Store, image, lktImage string, sysCtx *types.SystemContext) error {
	src, err := istorage.Transport.ParseStoreReference(store, image)
	if err != nil {
		return err
	}
	platform, err := imagePlatform(ctx, src, sysCtx)
	if err != nil {
		return err
	}
	found, err := inLinuxkitCache(ctx, lktImage, platform.Architecture)
	if err != nil || found {
		return err
	}
	dst, err := ocilayout.NewReference(linuxkitCacheDir(), lktImage+"-"+platform.Architecture)
	if err != nil {
		return err
	}
	policy, err := signature.NewPolicyContext(&signature.Policy{
		Default: signature.PolicyRequirements{signature.NewPRInsecureAcceptAnything()},
	})
	if err != nil {
		return err
	}
	defer func() { _ = policy.Destroy() }()
	blob, err := imgcopy.Image(ctx, policy, dst, src, &imgcopy.Options{
		ReportWriter:   os.Stdout,
		SourceCtx:      sysCtx,
		DestinationCtx: sysCtx,
	})
	if err != nil {
		return err
	}
	digest, err := manifest.Digest(blob)
	if err != nil {
		return err
	}
	return linuxkitIndexPut(ctx, lktImage, imgspecv1.Descriptor{
		MediaType: manifest.GuessMIMEType(blob),
		Digest:    digest,
		Size:      int64(len(blob)),
		Platform:  platform,
	}, sysCtx)
}

// imagePlatform returns the OS and architecture recorded in an image's config.
func imagePlatform(ctx context.Context, ref types.ImageReference, sysCtx *types.SystemContext) (*imgspecv1.Platform, error) {
	img, err := ref.NewImage(ctx, sysCtx)
	if err != nil {
		return nil, err
	}
	defer img.Close()
	info, err := img.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	return &imgspecv1.Platform{OS: info.Os, Architecture: info.Architecture}, nil
}

// linuxkitIndexPut makes name an index in the linuxkit cache that holds desc
// for its platform, next to the images the index already has for others.
func linuxkitIndexPut(ctx context.Context, name string, desc imgspecv1.Descriptor, sysCtx *types.SystemContext) error {
	ref, err := ocilayout.NewReference(linuxkitCacheDir(), name)
	if err != nil {
		return err
	}
	var index imgspecv1.Index
	if old, err := ref.NewImageSource(ctx, sysCtx); err == nil {
		blob, mimeType, err := old.GetManifest(ctx, nil)
		old.Close()
		if err == nil && mimeType == imgspecv1.MediaTypeImageIndex {
			if err := json.Unmarshal(blob, &index); err != nil {
				return err
			}
		}
	}
	index.SchemaVersion = 2
	index.MediaType = imgspecv1.MediaTypeImageIndex
	i := slices.IndexFunc(index.Manifests, func(m imgspecv1.Descriptor) bool {
		return m.Platform != nil && m.Platform.OS == desc.Platform.OS && m.Platform.Architecture == desc.Platform.Architecture
	})
	if i < 0 {
		index.Manifests = append(index.Manifests, desc)
	} else {
		index.Manifests[i] = desc
	}
	blob, err := json.Marshal(index)
	if err != nil {
		return err
	}
	dst, err := ref.NewImageDestination(ctx, sysCtx)
	if err != nil {
		return err
	}
	defer dst.Close()
	if err := dst.PutManifest(ctx, blob, nil); err != nil {
		return err
	}
	return dst.Commit(ctx, nil)
}

// inLinuxkitCache reports whether linuxkit pkg build finds lktImage, the full
// docker.io/<org>/<name>:<tag> name, in its cache for arch and so skips it.
// Like linuxkit's cache.ImageInCache: the index.json entry named lktImage is
// a plain image, which counts for every arch, or an index with an image for
// linux/arch, and that image's manifest, config and layer blobs all exist.
func inLinuxkitCache(ctx context.Context, lktImage, arch string) (bool, error) {
	absent := func(err error) (bool, error) {
		var notFound ocilayout.ImageNotFoundError
		if errors.As(err, &notFound) || errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	dir := linuxkitCacheDir()
	ref, err := ocilayout.NewReference(dir, lktImage)
	if err != nil {
		return false, err
	}
	src, err := ref.NewImageSource(ctx, nil)
	if err != nil {
		return absent(err)
	}
	defer src.Close()
	blob, mimeType, err := src.GetManifest(ctx, nil)
	if err != nil {
		return absent(err)
	}
	if manifest.MIMETypeIsMultiImage(mimeType) {
		list, err := manifest.ListFromBlob(blob, mimeType)
		if err != nil {
			return false, err
		}
		instances := list.Instances()
		i := slices.IndexFunc(instances, func(d digest.Digest) bool {
			u, err := list.Instance(d)
			p := u.ReadOnly.Platform
			return err == nil && p != nil && p.OS == "linux" && p.Architecture == arch && !manifest.MIMETypeIsMultiImage(u.MediaType)
		})
		if i < 0 {
			return false, nil
		}
		if blob, _, err = src.GetManifest(ctx, &instances[i]); err != nil {
			return absent(err)
		}
		mimeType = manifest.GuessMIMEType(blob)
	}
	m, err := manifest.FromBlob(blob, mimeType)
	if err != nil {
		return false, err
	}
	blobs := []types.BlobInfo{m.ConfigInfo()}
	for _, l := range m.LayerInfos() {
		blobs = append(blobs, l.BlobInfo)
	}
	for _, b := range blobs {
		if _, err := os.Stat(filepath.Join(dir, "blobs", b.Digest.Algorithm().String(), b.Digest.Encoded())); err != nil {
			return absent(err)
		}
	}
	return true, nil
}

func exportAllToLinuxkitCache() error {
	unshare.MaybeReexecUsingUserNamespace(false)

	store, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _, _ = store.Shutdown(false) }()

	images, err := store.Images()
	if err != nil {
		return err
	}

	sysCtx, err := registriesConf()
	if err != nil {
		panic(err)
	}

	for _, img := range images {
		if len(img.Names) == 0 {
			continue
		}
		if len(img.Names) > 1 {
			fmt.Printf("img has more than 1 name: %+v\n", img)
			panic("")
		}
		labels := imageLabels(store, img.ID)
		lktImageName := labels[lktNamePrefix]
		if lktImageName == "" {
			continue
		}
		fmt.Printf("%s -> %s ...\n", img.Names[0], lktImageName)
		err = exportToLinuxkitCache(context.TODO(), store, img.Names[0], lktImageName, sysCtx)
		if err != nil {
			return err
		}
	}
	return nil
}
