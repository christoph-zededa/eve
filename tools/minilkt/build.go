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

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"go.podman.io/buildah"
	"go.podman.io/buildah/define"
	"go.podman.io/buildah/imagebuildah"
	"go.podman.io/common/pkg/auth"
	"go.podman.io/image/v5/docker/reference"
	"go.podman.io/image/v5/types"
	"go.podman.io/storage"
	"go.podman.io/storage/pkg/homedir"
	"go.podman.io/storage/pkg/unshare"
)

const orgBuildArgPrefix = "org.minilkt.buildarg"
const lktNamePrefix = "linuxkit.image.name"
const miniLktNamePrefix = "minilkt.image.name"

var dataDir = filepath.Join(homedir.Get(), ".minilkt")

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
	return filepath.Join(runtimeDir, "minilkt"), filepath.Join(dataDir, "container", "storage"), nil
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

type pkg struct {
	imageName        string // key
	path             string
	lktHash          string
	miniLktHash      string
	pkgDependencies  []string
	dockerFileImages []string
	by               *BuildYAML
	buildArgsDeps    map[string]string
}

func newPkgs(ds []string) map[string]pkg {
	pkgs := map[string]*pkg{}

	for _, d := range ds {
		var err error
		p := pkg{path: d}

		p.lktHash, err = lktPkgHash(d)
		if err != nil {
			panic(err)
		}
		p.by, err = parseBuildYAMLDir(d)
		if err != nil {
			panic(err)
		}
		p.imageName = fmt.Sprintf("%s/%s", p.by.Org, p.by.Image)

		p.dockerFileImages, err = dockerfileImagesFromPath(filepath.Join(p.path, "Dockerfile"), map[string]string{})
		if err != nil {
			panic(err)
		}

		pkgs[p.imageName] = &p
	}

	for _, p := range pkgs {
		var err error

		depPkgs := make([]*pkg, 0)
		p.pkgDependencies = make([]string, 0)
		for _, dep := range p.dockerFileImages {
			pDep, found := pkgs[dep]
			if !found {
				continue
				// panic("could not find " + dep)
			}
			p.pkgDependencies = append(p.pkgDependencies, pDep.imageName)
			depPkgs = append(depPkgs, pDep)
		}
		extraPaths := make([]string, 0, len(depPkgs))
		for _, p := range depPkgs {
			extraPaths = append(extraPaths, p.path)
		}
		p.miniLktHash, err = lktPkgHash(p.path, extraPaths...)
		if err != nil {
			panic(err)
		}

	}

	// iterate again as we need the buildArgs
	for _, p := range pkgs {
		p.buildArgsDeps = make(map[string]string)
		for _, pDepName := range p.pkgDependencies {
			pDep, found := pkgs[pDepName]
			if !found {
				panic("could not find " + pDepName)
			}

			byOrg := strings.ToUpper(pDep.by.Org)
			byImage := strings.ToUpper(pDep.by.Image)
			byOrg = strings.ReplaceAll(byOrg, "-", "_")
			byImage = strings.ReplaceAll(byImage, "-", "_")
			envVarKey := fmt.Sprintf("%s_%s_AUTO_TAG", byOrg, byImage)
			envVarVal := fmt.Sprintf("%s", pDep.miniLktHash)

			p.buildArgsDeps[envVarKey] = envVarVal
		}
	}

	pkgsMap := make(map[string]pkg)
	for _, p := range pkgs {
		pkgsMap[p.imageName] = *p
	}
	return pkgsMap
}

func login() error {
	sysCtx, err := registriesConf()
	if err != nil {
		return err
	}
	opts := &auth.LoginOptions{
		StdinPassword: false,
		Stdout:        os.Stdout,
		Stdin:         os.Stdin,
	}
	return auth.Login(context.TODO(), sysCtx, opts, []string{"docker.io"})
}

func buildMulti(ds []string, forceRebuild, dryBuild, keepGoing, offlineBuild bool) {
	unshare.MaybeReexecUsingUserNamespace(false)

	// err := login()
	// if err != nil {
	// 	panic(err)
	// }
	pkgs := newPkgs(ds)

	deps := map[string][]string{}
	for _, p := range pkgs {
		deps[p.imageName] = p.pkgDependencies
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
		pkg := pkgs[image]

		err := build(
			pkg,
			httpsProxyPort,
			p.CertPEM,
			buildNetns.Path,
			sysCtx,
			forceRebuild,
			dryBuild,
		)
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
				fmt.Printf("!!!!! building %s failed: %T -> %v\n", image, lastErr, lastErr)
			}
		}
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

func build(p pkg, httpsProxyPort int, certFile, netnsPath string, sysCtx *types.SystemContext, forceRebuild, dryBuild bool) error {
	fmt.Printf("----- building %s ------\n", p.imageName)
	unshare.MaybeReexecUsingUserNamespace(false)

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

	lktHash := p.lktHash
	miniLktHash := p.miniLktHash
	if p.by.Tag != "" {
		lktHash = p.by.tag(p.lktHash)
		miniLktHash = p.by.tag(p.miniLktHash)
	}
	lktImageName := fmt.Sprintf("%s/%s:%s", p.by.Org, p.by.Image, lktHash)
	miniLktImageName := fmt.Sprintf("%s/%s:%s", p.by.Org, p.by.Image, miniLktHash)
	lktFullName := qualified(lktImageName)
	miniLktFullName := qualified(miniLktImageName)
	fmt.Printf("image is: %s (lkt name is: %s)\n", miniLktFullName, lktFullName)

	// Every image the Dockerfile names reaches buildah fully qualified, so no
	// short name is ever resolved against the host's registries.conf.
	// additionalBuildCtx := map[string]*define.AdditionalBuildContext{}
	// for k, v := range replaceImage {
	// 	additionalBuildCtx[k] = &define.AdditionalBuildContext{
	// 		IsImage: true,
	// 		Value:   qualified(v),
	// 	}
	// }
	// for _, img := range dfImages {
	// 	if _, replaced := additionalBuildCtx[img]; !replaced {
	// 		additionalBuildCtx[img] = &define.AdditionalBuildContext{
	// 			IsImage: true,
	// 			Value:   qualified(img),
	// 		}
	// 	}
	// }
	newImageAnnotations := make([]string, 0)
	for k, v := range p.buildArgsDeps {
		addTag := fmt.Sprintf("%s.%s.%s", orgBuildArgPrefix, k, v)
		newImageAnnotations = append(newImageAnnotations, addTag)
	}
	newImageAnnotations = append(newImageAnnotations, fmt.Sprintf("%s=%s", lktNamePrefix, lktFullName))
	newImageAnnotations = append(newImageAnnotations, fmt.Sprintf("%s=%s", miniLktNamePrefix, miniLktFullName))
	if len(p.by.BuildArgs) > 0 {
		fmt.Printf("adding extra build args from build.yml: %+v\n", p.by.BuildArgs)
	}
	for k, v := range p.by.buildArgs() {
		p.buildArgsDeps[k] = v
	}

	buildArgs["TARGETARCH"] = "amd64"
	buildArgs["BUILDARCH"] = "amd64"

	stdoutWriter := prefixWriter{
		prefix: p.imageName + ": ",
		w:      os.Stdout,
	}
	stderrWriter := prefixWriter{
		prefix: "!!ERROR " + p.imageName + ": ",
		w:      os.Stdout,
	}
	defer stdoutWriter.Flush()
	defer stderrWriter.Flush()
	buildOptions := define.BuildOptions{
		ContextDirectory: p.path,
		NamespaceOptions: []define.NamespaceOption{
			{Name: string(specs.NetworkNamespace), Path: netnsPath},
		},
		RemoveIntermediateCtrs: false,
		Layers:                 true,
		// OutputFormat:           define.OCIv1ImageManifest,
		// Isolation: define.IsolationOCIRootless, // TODO
		OutputFormat:    define.Dockerv2ImageManifest,
		PullPolicy:      define.PullIfMissing,
		SystemContext:   sysCtx,
		CommonBuildOpts: &define.CommonBuildOptions{HTTPProxy: true},
		TransientMounts: []string{
			certFile + ":/etc/ssl/cert.pem:ro,z",
			certFile + ":/usr/local/share/ca-certificates/proxy-ca.crt:ro,z",
		},
		// AdditionalBuildContexts: additionalBuildCtx,
		// AdditionalTags:          []string{imageName},
		Labels: newImageAnnotations,
		Output: miniLktFullName,
		Args:   p.buildArgsDeps,
		Out:    &stdoutWriter,
		Err:    &stderrWriter,
		Log: func(format string, args ...any) {
			fmt.Fprintf(&stdoutWriter, format+"\n", args...)
		},
	}
	if forceRebuild {
		buildOptions.NoCache = true
	}

	if !forceRebuild {
		imageID, err := buildah.Pull(context.TODO(), miniLktFullName, buildah.PullOptions{
			SystemContext: sysCtx,
			Store:         buildStore,
		})
		if err == nil {
			fmt.Printf("successfully pulled image id: %s\n", imageID)
			imageID, err = addImageAnnotations(buildStore, sysCtx, imageID, newImageAnnotations)
			if err != nil {
				return err
			}
			return nil
		}

		fmt.Printf("pull failed (proxy port: %d): %+v\n", httpsProxyPort, err)
		imageID, err = buildah.Pull(context.TODO(), lktFullName, buildah.PullOptions{
			SystemContext: sysCtx,
			Store:         buildStore,
		})
		img, err := buildStore.Image(imageID)
		if err == nil {
			oldImageAnnotations := additionalImageLabelsFromLabels(imageLabels(buildStore, img.ID))
			if compareAnnotations(oldImageAnnotations, newImageAnnotations) {
				fmt.Printf("Image (%s) already built, but needs re-tagging to minilkt: %s\n", lktFullName, miniLktFullName)
				err := buildStore.AddNames(img.ID, []string{miniLktFullName})
				if err != nil {
					panic(err)
				}
				return nil
			}
			fmt.Printf("Image needs to be rebuilt as dependency package(s) changed\n")
			buildOptions.NoCache = true

		}
	}

	if dryBuild {
		fmt.Println("dry build selected")
		return nil
	}
	buildImageID, _, err := imagebuildah.BuildDockerfiles(context.TODO(), buildStore, buildOptions, filepath.Join(p.path, "Dockerfile"))
	if err != nil {
		return fmt.Errorf("building '%s' failed (%T):\n%+v", p.path, err, err)
	}
	fmt.Printf("built image id: %s\n\ttag: %s\n\tannotations: %+v\n", buildImageID, miniLktImageName, newImageAnnotations)

	return nil
}

// prefixWriter prefixes every line; an incomplete last line is held back
// until its newline arrives or Flush is called.
type prefixWriter struct {
	prefix string
	w      io.Writer
	buf    []byte
}

func (pw *prefixWriter) Write(p []byte) (n int, err error) {
	pw.buf = append(pw.buf, p...)
	for {
		i := bytes.IndexByte(pw.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := bytes.TrimRight(pw.buf[:i], "\r")
		if _, err := fmt.Fprintf(pw.w, "%s%s\n", pw.prefix, line); err != nil {
			return 0, err
		}
		pw.buf = pw.buf[i+1:]
	}
}

func (pw *prefixWriter) Flush() {
	if len(pw.buf) > 0 {
		fmt.Fprintf(pw.w, "%s%s\n", pw.prefix, pw.buf)
		pw.buf = nil
	}
}

// return true if same
func compareAnnotations(old, new []string) bool {
	if len(old) == 0 && len(new) == 0 {
		return true
	}

	new = slices.DeleteFunc(new, func(line string) bool {
		return !strings.HasPrefix(line, orgBuildArgPrefix)
	})
	old = slices.DeleteFunc(old, func(line string) bool {
		return !strings.HasPrefix(line, orgBuildArgPrefix)
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
		panic(fmt.Sprintf("parsing %s failed: %v", name, err))
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
