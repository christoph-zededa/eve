package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"minilkt/dockerproxy"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"go.podman.io/buildah"
	"go.podman.io/buildah/define"
	"go.podman.io/buildah/imagebuildah"
	"go.podman.io/image/v5/docker/reference"
	"go.podman.io/image/v5/types"
	"go.podman.io/storage"
	"go.podman.io/storage/pkg/homedir"
	"go.podman.io/storage/pkg/unshare"
)

const orgImageAdditionPrefix = "org.minilkt.additionalimage"
const dataDir = "/home/christoph/.minilkt"

// openStore opens the image store that build writes to and list reads.
func openStore() (storage.Store, error) {
	opts, err := storage.DefaultStoreOptions()
	if err != nil {
		return nil, err
	}
	if err := setStoreOptions(&opts); err != nil {
		return nil, err
	}
	return storage.GetStore(opts)
}

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

func buildMulti(ds []string, forceRebuild, dryBuild, keepGoing, offlineBuild bool) {
	// The build network namespace below needs the user namespace, so re-exec
	// first; the later call in build is then a no-op.
	unshare.MaybeReexecUsingUserNamespace(false)

	deps := map[string][]string{}
	image2pkgHashes := map[string]string{}
	image2path := map[string]string{}
	for _, d := range ds {
		hash, err := lktPkgHash(d)
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

		dfImages, err := dockerfileImagesFromPath(filepath.Join(d, "Dockerfile"), map[string]string{})
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

	p, err := dockerproxy.New(filepath.Join(dataDir, "proxy"))
	if err != nil {
		panic(err)
	}
	p.Verbose = true
	p.BlockExtern = offlineBuild
	httpsProxyPort, err := getFreePort()
	if err != nil {
		panic(err)
	}
	// Build steps run in a network namespace holding nothing but loopback and
	// the proxy, so the proxy is the only thing they can reach. minilkt's own
	// pulls and the host-side git of ADD use the same port on the host side.
	buildNetns, err := newPrivateNetns()
	if err != nil {
		panic(err)
	}
	listeners, err := listenBothSides(buildNetns, uint16(httpsProxyPort))
	if err != nil {
		panic(err)
	}
	if err := p.StartWith(listeners); err != nil {
		panic(err)
	}
	defer p.Stop()

	certFile := p.CertPEM
	os.Setenv("SSL_CERT_FILE", certFile)

	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", httpsProxyPort)

	os.Setenv("HTTPS_PROXY", proxyURL)
	os.Setenv("HTTP_PROXY", proxyURL)
	os.Setenv("https_proxy", proxyURL)
	os.Setenv("http_proxy", proxyURL)
	os.Setenv("NO_PROXY", "localhost,127.0.0.1")

	setupGit(certFile)
	sysCtx, err := registriesConf()
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

		// fmt.Printf(">>>>> %s\n", d)
		// fmt.Printf("\t%+v\n", replaceImage)
		err := build(d, replaceImage, httpsProxyPort, p.CertPEM, buildNetns.Path, sysCtx, forceRebuild, dryBuild)
		if err != nil {
			var lastErr error
			for {
				unWrapErr := errors.Unwrap(err)
				if unWrapErr == nil {
					break
				}
				lastErr = unWrapErr
			}
			if lastErr != nil {
				panic(err)
			}
			if !keepGoing {
				panic(err)
			} else {
				fmt.Printf("!!!!! building %s failed: %T -> %v\n", d, lastErr, lastErr)
			}
		}
		// fmt.Printf("<<<<<<<<<<<<<\n")
	}
}

func getFreePort() (port int, err error) {
	var a *net.TCPAddr
	if a, err = net.ResolveTCPAddr("tcp", "localhost:0"); err == nil {
		var l *net.TCPListener
		if l, err = net.ListenTCP("tcp", a); err == nil {
			defer l.Close()
			return l.Addr().(*net.TCPAddr).Port, nil
		}
	}
	return
}

func buildYamlTag(by *BuildYAML, hash string) string {
	tmpl, err := template.New("tag").Parse(by.Tag)
	if err != nil {
		return hash
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"Hash": hash}); err != nil {
		return hash
	}
	return buf.String()
}

func build(d string, replaceImage map[string]string, httpsProxyPort int, certFile, netnsPath string, sysCtx *types.SystemContext, forceRebuild, dryBuild bool) error {
	fmt.Printf("----- building %s ------\n", d)
	unshare.MaybeReexecUsingUserNamespace(false)

	dockerfile := filepath.Join(d, "Dockerfile")
	// parse(dockerfile)
	dfImages, err := dockerfileImagesFromPath(dockerfile, map[string]string{})
	if err != nil {
		return fmt.Errorf("parsing dockerfile %s failed: %v", dockerfile, err)
	}

	fmt.Printf("dfImages: %+v\n", dfImages)

	buildStore, err := openStore()
	if err != nil {
		return fmt.Errorf("open image store failed: %v", err)
	}
	defer func() {
		if _, err := buildStore.Shutdown(false); err != nil {
			if !errors.Is(err, storage.ErrLayerUsedByContainer) {
				fmt.Printf("failed to shutdown storage: %q", err)
			}
		}
	}()

	imageTagHash, err := lktPkgHash(d)
	if err != nil {
		panic(err)
	}
	by, err := ParseBuildYAMLDir(d)
	if err != nil {
		panic(err)
	}
	if by.Tag != "" {
		imageTagHash = buildYamlTag(by, imageTagHash)
	}
	imageName := fmt.Sprintf("%s/%s:%s", by.Org, by.Image, imageTagHash)
	fullName := qualified(imageName)
	fmt.Printf("image is: %s\n", fullName)

	// Every image the Dockerfile names reaches buildah fully qualified, so no
	// short name is ever resolved against the host's registries.conf.
	additionalBuildCtx := map[string]*define.AdditionalBuildContext{}
	for k, v := range replaceImage {
		additionalBuildCtx[k] = &define.AdditionalBuildContext{
			IsImage: true,
			Value:   qualified(v),
		}
	}
	for _, img := range dfImages {
		if _, replaced := additionalBuildCtx[img]; !replaced {
			additionalBuildCtx[img] = &define.AdditionalBuildContext{
				IsImage: true,
				Value:   qualified(img),
			}
		}
	}
	newImageAnnotations := make([]string, 0)
	for _, v := range replaceImage {
		addTag := fmt.Sprintf("%s.%s", orgImageAdditionPrefix, v)
		newImageAnnotations = append(newImageAnnotations, addTag)

	}
	buildArgs := by.buildArgs()
	if len(buildArgs) > 0 {
		fmt.Printf("adding extra build args: %+v\n", buildArgs)
	}

	buildArgs["TARGETARCH"] = "amd64"
	buildArgs["BUILDARCH"] = "amd64"

	buildOptions := define.BuildOptions{
		ContextDirectory: d,
		NamespaceOptions: []define.NamespaceOption{
			{Name: string(specs.NetworkNamespace), Path: netnsPath},
		},
		RemoveIntermediateCtrs: false,
		Layers:                 true,
		// OutputFormat:           define.OCIv1ImageManifest,
		OutputFormat:    define.Dockerv2ImageManifest,
		PullPolicy:      define.PullIfMissing,
		SystemContext:   sysCtx,
		CommonBuildOpts: &define.CommonBuildOptions{HTTPProxy: true},
		TransientMounts: []string{
			certFile + ":/etc/ssl/cert.pem:ro,z",
			certFile + ":/usr/local/share/ca-certificates/proxy-ca.crt:ro,z",
		},
		AdditionalBuildContexts: additionalBuildCtx,
		// AdditionalTags:          []string{imageName},
		Labels: newImageAnnotations,
		Output: fullName,
		Args:   buildArgs,
	}
	if forceRebuild {
		buildOptions.NoCache = true
	}

	if !forceRebuild {
		imageID, err := buildah.Pull(context.TODO(), fullName, buildah.PullOptions{
			SystemContext: sysCtx,
			Store:         buildStore,
		})
		if err != nil {
			fmt.Printf("pull failed (proxy port: %d): %T %+v\n", httpsProxyPort, err, err)
			// TODO: removeme
			_, err := buildStore.Image(fullName)
			if err == nil {
				panic("cannot pull, but image is there, please fix")
			}
			// could not pull image, therefore we cannot lookup extra annotations
			buildOptions.NoCache = true
		} else {
			fmt.Printf("pull image id: %s\n", imageID)
			// return
			img, err := buildStore.Image(imageID)
			if err != nil {
				panic(err)
			}
			oldImageAnnotations := annotationList(imageLabels(buildStore, img.ID))

			if compareAnnotations(oldImageAnnotations, newImageAnnotations) {
				fmt.Printf("Image (%s) already built\n", fullName)
				return nil
			} else {
				fmt.Printf("Image needs to be rebuilt as dependency package(s) changed\n")
				buildOptions.NoCache = true
			}
		}
	}

	if dryBuild {
		fmt.Println("dry build selected")
		return nil
	}
	// TODO: inject TARGETARCH and BUILDARCH env variables
	buildImageID, _, err := imagebuildah.BuildDockerfiles(context.TODO(), buildStore, buildOptions, dockerfile)
	if err != nil {
		return fmt.Errorf("building '%s' failed (%T):\n%+v", d, err, err)
	}
	fmt.Printf("built image id: %s\n\ttag: %s\n\tannotations: %+v\n", buildImageID, imageName, newImageAnnotations)

	return nil
}

// return true if same
func compareAnnotations(old, new []string) bool {
	if len(old) == 0 && len(new) == 0 {
		return true
	}

	new = slices.DeleteFunc(new, func(line string) bool {
		return !strings.HasPrefix(line, orgImageAdditionPrefix)
	})
	old = slices.DeleteFunc(old, func(line string) bool {
		return !strings.HasPrefix(line, orgImageAdditionPrefix)
	})

	slices.Sort(old)
	slices.Sort(new)

	if slices.Equal(old, new) {
		return true
	}

	fmt.Println("old image annotations:")
	for _, annotation := range old {
		fmt.Printf("- old additional tag: %s\n", annotation)
	}
	fmt.Println("new image annotations:")
	for _, annotation := range new {
		fmt.Printf("- new additional tag: %s\n", annotation)
	}

	return false
}

// qualified returns name as a full reference with Docker's defaults, so
// lfedge/eve-alpine:x becomes docker.io/lfedge/eve-alpine:x and alpine becomes
// docker.io/library/alpine:latest. It panics on an invalid reference, like
// splitImage.
func qualified(name string) string {
	named, err := reference.ParseNormalizedNamed(name)
	if err != nil {
		panic(err)
	}
	return reference.TagNameOnly(named).String()
}

func setupGit(certFile string) {
	var err error

	gitconfig := `
#!/bin/sh
# minilkt: give buildah's git ADD (init, fetch --depth=1 <ref>, checkout
# FETCH_HEAD) what BuildKit's ADD <git url> provides: a tag or branch ref is
# kept as a tag, a commit ref gets the full history and all tags, so git
# describe works either way, and submodules are initialized.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
if [ -z "$(git rev-parse --show-superproject-working-tree)" ]; then
        gitdir=$(git rev-parse --git-dir)
        ref=$(sed -n "s/^[0-9a-f]*\t[^\t]*\t\(tag\|branch\) '\([^']*\)' of .*/\2/p" "$gitdir/FETCH_HEAD" 2>/dev/null | head -n 1)
        if [ -n "$ref" ]; then
                git rev-parse -q --verify "$ref^{commit}" >/dev/null 2>&1 || git tag "$ref" HEAD
        elif [ -f "$gitdir/FETCH_HEAD" ]; then
                unshallow=
                [ -f "$gitdir/shallow" ] && unshallow=--unshallow
                git fetch --quiet --tags $unshallow origin "$(git rev-parse HEAD)"
        fi
fi
exec git submodule update --init --recursive --depth=1
	`
	err = os.MkdirAll(filepath.Join(dataDir, "githooks"), 0o700)
	if err != nil {
		panic(err)
	}
	gitPostCheckoutScriptPath := filepath.Join(dataDir, "githooks", "post-checkout")
	err = os.WriteFile(gitPostCheckoutScriptPath, []byte(gitconfig), 0o700)
	if err != nil {
		panic(err)
	}

	os.Setenv("GIT_CONFIG_COUNT", "1")
	os.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	os.Setenv("GIT_CONFIG_VALUE_0", filepath.Join(dataDir, "githooks"))
	os.Setenv("GIT_CONFIG_GLOBAL", "/dev/null") // keep developer URL rewrites and helpers out of ADD clones
	os.Setenv("GIT_SSL_CAINFO", certFile)       // git ignores SSL_CERT_FILE; trust the proxy CA explicitly
}
