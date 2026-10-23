package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"minilkt/dockerproxy"
	"os"
	"path/filepath"
	"slices"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"go.podman.io/buildah"
	"go.podman.io/buildah/define"
	"go.podman.io/buildah/imagebuildah"
	"go.podman.io/image/v5/types"
	"go.podman.io/storage"
	"go.podman.io/storage/pkg/homedir"
	"go.podman.io/storage/pkg/unshare"
)

// setStoreOptions makes the image store usable by an unprivileged user.
func setStoreOptions(opts *storage.StoreOptions) error {
	runRoot, graphRoot, err := storeRoots()
	if err != nil {
		return err
	}
	if runRoot != "" {
		opts.RunRoot, opts.GraphRoot = runRoot, graphRoot
	}
	opts.GraphDriverOptions = slices.DeleteFunc(opts.GraphDriverOptions, unreadableStore)
	return nil
}

// TODO: use ~/.minilkt
// storeRoots returns the run and graph roots to build under, or empty strings
// to keep whatever the config files resolved to.
//
// Distros ship /usr/share/containers/storage.conf with the rootful
// runroot=/run/containers/storage and graphroot=/var/lib/containers/storage
// spelled out, and an explicit value in a config file beats the rootless
// defaults c/storage would otherwise derive from $XDG_RUNTIME_DIR — so a
// rootless build dies on "mkdir /run/containers: permission denied".
// MINILKT_STORAGE_ROOT relocates the whole store.
func storeRoots() (runRoot, graphRoot string, err error) {
	if root := os.Getenv("MINILKT_STORAGE_ROOT"); root != "" {
		return filepath.Join(root, "run"), filepath.Join(root, "storage"), nil
	}
	if unshare.GetRootlessUID() == 0 {
		return "", "", nil
	}
	runtimeDir, err := homedir.GetRuntimeDir()
	if err != nil {
		return "", "", fmt.Errorf("no runtime dir for the rootless store: %w", err)
	}
	dataDir, err := homedir.GetDataHome()
	if err != nil {
		return "", "", fmt.Errorf("no data dir for the rootless store: %w", err)
	}
	return filepath.Join(runtimeDir, "minilkt"), filepath.Join(dataDir, "minilkt", "storage"), nil
}

// unreadableStore reports whether a graph driver option names an additional
// image or layer store the current user cannot read. Distros point these at
// system stores such as /usr/lib/containers/storage, whose per-driver
// subdirectories are mode 0700 root.
func unreadableStore(opt string) bool {
	path, ok := strings.CutPrefix(opt, "imagestore=")
	if !ok {
		if path, ok = strings.CutPrefix(opt, "additionallayerstore="); !ok {
			return false
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return true
	}
	for _, e := range entries {
		f, err := os.Open(filepath.Join(path, e.Name()))
		if err != nil {
			return true
		}
		f.Close()
	}
	return false
}

func buildMulti(ds []string, forceRebuild bool) {
	deps := map[string][]string{}
	image2pkgHashes := map[string]string{}
	image2path := map[string]string{}
	for _, d := range ds {
		hash, err := PkgHash(d)
		if err != nil {
			panic(err)
		}
		by, err := ParseBuildYAMLDir(d)
		if err != nil {
			panic(err)
		}

		imageName := fmt.Sprintf("%s/%s", by.Org, by.Image)
		image2pkgHashes[imageName] = hash
		image2path[imageName] = d

		dfImages, err := DockerfileImages(filepath.Join(d, "Dockerfile"), map[string]string{})
		if err != nil {
			panic(err)
		}

		deps[imageName] = dfImages
	}

	fmt.Printf("deps: %+v\n", deps)

	if err := dumpBuildMulti(os.Stdout, deps); err != nil {
		panic(err)
	}
	order, err := buildOrder(deps)
	if err != nil {
		panic(err)
	}

	for _, image := range order {
		replaceImage := map[string]string{}
		d, found := image2path[image]

		dDeps := deps[image]
		for _, dep := range dDeps {
			org, name, _ := splitImage(dep)
			newImage := fmt.Sprintf("%s/%s", org, name)
			if image == newImage {
				continue
			}
			newImageHash := image2pkgHashes[newImage]
			if newImageHash == "" {
				continue
			}
			replaceImage[dep] = fmt.Sprintf("%s:%s", newImage, newImageHash)
		}

		if !found {
			log.Fatalf("could not find path for %s\n", image)
			continue
		}

		fmt.Printf(">>>>> %s\n", d)
		fmt.Printf("\t%+v\n", replaceImage)
		build(d, replaceImage, forceRebuild)
		fmt.Printf("<<<<<<<<<<<<<\n")
	}
}

func build(d string, replaceImage map[string]string, forceRebuild bool) {
	unshare.MaybeReexecUsingUserNamespace(false)

	dataDir := "/home/christoph/.minilkt"
	p, err := dockerproxy.New(filepath.Join(dataDir, "proxy"))
	p.Verbose = true
	if err := p.Start(8080); err != nil {
		panic(err)
	}
	defer p.Stop()

	certFile := p.CertPEM
	os.Setenv("SSL_CERT_FILE", certFile)

	os.Setenv("HTTPS_PROXY", "http://localhost:8080")
	os.Setenv("HTTP_PROXY", "http://localhost:8080")
	os.Setenv("NO_PROXY", "localhost,127.0.0.1")

	dockerfile := filepath.Join(d, "Dockerfile")
	// parse(dockerfile)
	dfImages, err := DockerfileImages(dockerfile, map[string]string{})
	if err != nil {
		panic(err)
	}

	fmt.Printf("dfImages: %+v\n", dfImages)

	buildStoreOptions, err := storage.DefaultStoreOptions()
	if err != nil {
		panic(err)
	}
	if err := setStoreOptions(&buildStoreOptions); err != nil {
		panic(err)
	}

	buildStore, err := storage.GetStore(buildStoreOptions)
	if err != nil {
		panic(err)
	}
	defer func() {
		if _, err := buildStore.Shutdown(false); err != nil {
			if !errors.Is(err, storage.ErrLayerUsedByContainer) {
				fmt.Printf("failed to shutdown storage: %q", err)
			}
		}
	}()

	hash, err := PkgHash(d)
	if err != nil {
		panic(err)
	}
	by, err := ParseBuildYAMLDir(d)
	if err != nil {
		panic(err)
	}
	imageName := fmt.Sprintf("%s/%s:%s", by.Org, by.Image, hash)
	fmt.Printf("image is: %s\n", imageName)

	additionalBuildCtx := map[string]*define.AdditionalBuildContext{}
	for k, v := range replaceImage {
		additionalBuildCtx[k] = &define.AdditionalBuildContext{
			IsImage: true,
			Value:   v,
		}
	}
	buildOptions := define.BuildOptions{
		ContextDirectory: d,
		NamespaceOptions: []define.NamespaceOption{
			{Name: string(specs.NetworkNamespace), Host: true},
			{Name: string(specs.NetworkNamespace), Path: "pasta:--splice-only,-T,8080"},
		},
		RemoveIntermediateCtrs: false,
		Layers:                 true,
		OutputFormat:           define.OCIv1ImageManifest,
		PullPolicy:             define.PullIfMissing,
		CommonBuildOpts:        &define.CommonBuildOptions{HTTPProxy: true},
		TransientMounts: []string{
			certFile + ":/etc/ssl/cert.pem:ro,z",
			certFile + ":/usr/local/share/ca-certificates/proxy-ca.crt:ro,z",
		},
		AdditionalBuildContexts: additionalBuildCtx,
		// AdditionalTags:          []string{imageName},
		Output: imageName,
	}
	panic("additionalBuildCtx does not make build again on change")
	if forceRebuild {
		buildOptions.NoCache = true
	}

	if !forceRebuild {
		imageID, err := buildah.Pull(context.TODO(), imageName, buildah.PullOptions{
			SystemContext: &types.SystemContext{},
			Store:         buildStore,
		})
		if err == nil {
			fmt.Printf("pull image id: %s\n", imageID)
			return
		}
	}
	buildImageID, _, err := imagebuildah.BuildDockerfiles(context.TODO(), buildStore, buildOptions, dockerfile)
	if err != nil {
		panic(err)
	}
	fmt.Printf("build image id: %s\ntag: %s\n", buildImageID, imageName)
}
