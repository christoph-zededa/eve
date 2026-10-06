package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/openshift/imagebuilder"
	"github.com/openshift/imagebuilder/dockerfile/parser"
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

func parse(dockerfilePath string) {

	bs, err := os.ReadFile(dockerfilePath)
	if err != nil {
		panic(err)
	}
	mainNode, err := imagebuilder.ParseDockerfile(bytes.NewReader(bs))
	// fmt.Printf("main node: %s\n", mainNode.Dump())
	dump(mainNode, 0)
}

func dump(n *parser.Node, depth int) {
	for i := 0; i < depth; i++ {
		fmt.Printf(" ")
	}

	fmt.Printf("val: %s\n", n.Value)

	for _, c := range n.Children {
		dump(c, depth+20)
		fmt.Println("-------------")
	}

	next := n.Next
	for next != nil {
		dump(next, depth+1)
		next = next.Next
	}

}

func main() {
	if buildah.InitReexec() {
		return
	}
	unshare.MaybeReexecUsingUserNamespace(false)

	d, _ := os.Getwd()
	dockerfile := filepath.Join(d, "Dockerfile")
	parse(dockerfile)

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

	// d, err := os.MkdirTemp("", "")
	// if err != nil {
	// 	panic(err)
	// }
	// defer os.RemoveAll(d)
	// dockerfile := filepath.Join(d, "Dockerfile")
	// f, err := os.Create(dockerfile)
	// if err != nil {
	// 	panic(err)
	// }
	// fmt.Fprintf(f, "FROM quay.io/libpod/alpine\nRUN echo CUT START; find /sys/fs/cgroup -print | sort ; echo CUT END")
	// f.Close()

	buildOptions := define.BuildOptions{
		ContextDirectory: d,
		NamespaceOptions: []define.NamespaceOption{
			{Name: string(specs.NetworkNamespace), Host: true},
		},
		RemoveIntermediateCtrs: false,
		Layers:                 true,
		OutputFormat:           define.OCIv1ImageManifest,
		PullPolicy:             define.PullIfMissing,
	}

	hash, err := PkgHash(d)
	if err != nil {
		panic(err)
	}
	fmt.Printf("lkt like hash is: %s\n", hash)
	imageName := fmt.Sprintf("lfedge/eve-debug:%s", hash)
	imageID, err := buildah.Pull(context.TODO(), imageName, buildah.PullOptions{
		SystemContext: &types.SystemContext{},
		Store:         buildStore,
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("pull image id: %s\n", imageID)
	buildImageID, _, err := imagebuildah.BuildDockerfiles(context.TODO(), buildStore, buildOptions, dockerfile)
	if err != nil {
		panic(err)
	}
	fmt.Printf("build image id: %s\n", buildImageID)
}
