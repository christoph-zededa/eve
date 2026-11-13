package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	imgcopy "go.podman.io/image/v5/copy"
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
// cache, an OCI layout dir, under the name lktImage. The ref.name annotation
// c/image writes is the one linuxkit looks images up by;
// io.containerd.image.name is only read by `linuxkit cache import`, which this
// bypasses.
func exportToLinuxkitCache(ctx context.Context, store storage.Store, image, lktImage string, sysCtx *types.SystemContext) error {
	src, err := istorage.Transport.ParseStoreReference(store, image)
	if err != nil {
		return err
	}
	dst, err := ocilayout.NewReference(linuxkitCacheDir(), lktImage)
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
	_, err = imgcopy.Image(ctx, policy, dst, src, &imgcopy.Options{
		ReportWriter:   os.Stdout,
		SourceCtx:      sysCtx,
		DestinationCtx: sysCtx,
	})
	return err
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
