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
	return filepath.Join(home, ".linuxkit", "cache")
}

// exportToLinuxkitCache copies image from the build store into the linuxkit
// cache, an OCI layout dir. The ref.name annotation c/image writes is the one
// linuxkit looks images up by; io.containerd.image.name is only read by
// `linuxkit cache import`, which this bypasses.
func exportToLinuxkitCache(ctx context.Context, store storage.Store, image string, sysCtx *types.SystemContext) error {
	src, err := istorage.Transport.ParseStoreReference(store, image)
	if err != nil {
		return err
	}
	dst, err := ocilayout.NewReference(linuxkitCacheDir(), image)
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

	sysCtx, err := registriesConf(dataDir)
	if err != nil {
		panic(err)
	}

	for i, img := range images {
		if len(img.Names) == 0 {
			continue
		}
		if len(img.Names) > 1 {
			fmt.Printf("img has more than 1 name: %+v\n", img)
			panic("")
		}
		if i > 1 {
			fmt.Println()
		}
		fmt.Printf("%s ...\n", img.Names[0])
		err = exportToLinuxkitCache(context.TODO(), store, img.Names[0], sysCtx)
		if err != nil {
			return err
		}
	}
	return nil
}
