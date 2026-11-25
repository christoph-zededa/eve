package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"runtime"
	"sync"

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
	"go.podman.io/common/libimage/manifests"
	"go.podman.io/common/pkg/auth"
	"go.podman.io/image/v5/docker/reference"
	istorage "go.podman.io/image/v5/storage"
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
	pkgDependencies  map[string]struct{}
	dockerFileImages []dockerfileImage
	by               *BuildYAML
	buildArgsDeps    map[string]string
	targetArch       string
}

// newPkgs returns the builds the packages in ds need for targetArch: every
// package for targetArch, and in a cross build also, for the build
// architecture, the packages that stages start from with
// --platform=$BUILDPLATFORM and the packages those are built from. A build's
// imageName, the key, is "<arch>/<image>"; pkgDependencies name builds by it.
func newPkgs(bys []string, targetArch string) map[string]pkg {
	pkgs := map[string]*pkg{}
	var pkgsMutex sync.Mutex

	var wg sync.WaitGroup

	for _, by := range bys {
		d := filepath.Dir(by)
		wg.Go(func() {
			var err error
			p := pkg{path: d}

			p.lktHash, err = lktPkgHash(d)
			if err != nil {
				panic(err)
			}
			p.by, err = parseBuildYAML(by)
			if err != nil {
				panic(err)
			}
			p.imageName = fmt.Sprintf("%s/%s", p.by.Org, p.by.Image)

			args := map[string]string{"TARGETPLATFORM": "linux/" + targetArch}
			p.dockerFileImages, err = dockerfileImagesFromPath(filepath.Join(p.path, "Dockerfile"), args)
			if err != nil {
				panic(err)
			}

			pkgsMutex.Lock()
			pkgs[p.imageName] = &p
			pkgsMutex.Unlock()
		})
	}

	wg.Wait()

	for _, p := range pkgs {
		p := p
		wg.Go(func() {
			var err error

			p.pkgDependencies = map[string]struct{}{}
			for _, dep := range p.dockerFileImages {
				pDep, found := pkgs[dep.name]
				if !found {
					continue
					// panic("could not find " + dep)
				}
				p.pkgDependencies[pDep.imageName] = struct{}{}
			}
			extraPaths := make([]string, 0, len(p.pkgDependencies))
			for dep := range p.pkgDependencies {
				extraPaths = append(extraPaths, pkgs[dep].path)
			}
			p.miniLktHash, err = lktPkgHash(p.path, extraPaths...)
			if err != nil {
				panic(err)
			}
		})
	}
	wg.Wait()

	// iterate again as we need the buildArgs
	for _, p := range pkgs {
		p.buildArgsDeps = make(map[string]string)
		for pDepName := range p.pkgDependencies {
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
	var add func(p *pkg, arch string)
	add = func(p *pkg, arch string) {
		key := arch + "/" + p.imageName
		if _, added := pkgsMap[key]; added {
			return
		}
		b := *p
		b.imageName = key
		b.targetArch = arch
		b.pkgDependencies = map[string]struct{}{}
		b.buildArgsDeps = maps.Clone(p.buildArgsDeps) // build adds to it
		pkgsMap[key] = b
		for _, image := range p.dockerFileImages {
			dep, isPkg := pkgs[image.name]
			if !isPkg {
				continue
			}
			depArch := arch
			if image.buildPlatform {
				depArch = runtime.GOARCH
			}
			b.pkgDependencies[depArch+"/"+image.name] = struct{}{}
			add(dep, depArch)
		}
	}
	for _, p := range pkgs {
		add(p, targetArch)
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

func buildMultiRootfsYaml(rootFSYamlPath string, bys []string, targetArch string, forceRebuild, dryBuild, keepGoing, offlineBuild bool) {
	ry := parseRootFSYaml(rootFSYamlPath)
	ryImgsMap := make(map[string]struct{})
	for _, img := range ry.images() {
		archImg := fmt.Sprintf("%s/%s", targetArch, img)
		ryImgsMap[archImg] = struct{}{}
	}

	buildPkgs(ryImgsMap, bys, targetArch, forceRebuild, dryBuild, keepGoing, offlineBuild)
}

func buildMulti(pkgs []string, bys []string, targetArch string, forceRebuild, dryBuild, keepGoing, offlineBuild bool) {
	buildPkgsMap := make(map[string]struct{})
	for _, bp := range pkgs {
		by, err := parseBuildYAML(bp)
		if err != nil {
			panic(err)
		}
		archImg := fmt.Sprintf("%s/%s/%s", targetArch, by.Org, by.Image)
		buildPkgsMap[archImg] = struct{}{}
	}

	buildPkgs(buildPkgsMap, bys, targetArch, forceRebuild, dryBuild, keepGoing, offlineBuild)
}

func buildPkgs(buildPkgs map[string]struct{}, bys []string, targetArch string, forceRebuild, dryBuild, keepGoing, offlineBuild bool) {
	unshare.MaybeReexecUsingUserNamespace(false)

	pkgs := newPkgs(bys, targetArch)
	relevantPkgs := make(map[string]pkg)
	var addDeps func(p pkg)
	addDeps = func(p pkg) {
		for depImg := range p.pkgDependencies {
			if _, added := relevantPkgs[depImg]; !added {
				relevantPkgs[depImg] = pkgs[depImg]
				addDeps(pkgs[depImg])
			}
		}
	}
	for _, pkg := range pkgs {
		_, found := buildPkgs[pkg.imageName]
		if !found {
			continue
		}
		relevantPkgs[pkg.imageName] = pkg
		addDeps(pkg)
	}

	deps := map[string][]string{}
	for _, p := range relevantPkgs {
		deps[p.imageName] = slices.Sorted(maps.Keys(p.pkgDependencies))
	}
	order, err := buildOrder(deps)
	if err != nil {
		panic(err)
	}
	if len(order) == 0 {
		fmt.Fprintf(os.Stderr, "nothing to build: %+v selects none of the given packages\n", buildPkgs)
		os.Exit(1)
	}
	fmt.Printf("pkgs to build: %+v\n", order)

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
	sysCtx.OSChoice = "linux"

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

	// targetArch := runtime.GOARCH
	targetArch := p.targetArch
	archCtx := *sysCtx
	archCtx.ArchitectureChoice = targetArch
	sysCtx = &archCtx
	p.buildArgsDeps["TARGETARCH"] = targetArch
	// p.buildArgsDeps["BUILDARCH"] = runtime.GOARCH
	p.buildArgsDeps["BUILDARCH"] = runtime.GOARCH
	// p.buildArgsDeps["BUILDPLATFORM"] = "linux/" + runtime.GOARCH

	logImageName := fmt.Sprintf("%s/%s", p.by.Org, p.by.Image)
	if targetArch != runtime.GOARCH {
		logImageName += fmt.Sprintf(" (%s)", targetArch)
	}
	stdoutWriter := prefixWriter{
		prefix: logImageName + ": ",
		w:      os.Stdout,
	}
	stderrWriter := prefixWriter{
		prefix: "[!E] " + logImageName + ": ",
		w:      os.Stdout,
	}
	defer stdoutWriter.Flush()
	defer stderrWriter.Flush()
	buildOptions := define.BuildOptions{
		Architecture:     targetArch,
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
		Output: miniLktFullName + "-" + targetArch,
		Args:   p.buildArgsDeps,
		Out:    &stdoutWriter,
		Err:    &stderrWriter,
		Log: func(format string, args ...any) {
			fmt.Fprintf(&stdoutWriter, format+"\n", args...)
		},
		RuntimeArgs: []string{"--root", filepath.Join(dataDir, "crun")},
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

	if err := addToManifestList(buildStore, miniLktFullName, buildImageID); err != nil {
		return fmt.Errorf("adding %s to the manifest list %s failed: %v", buildImageID, miniLktFullName, err)
	}
	return nil
}

// addToManifestList puts the image into the manifest list name, creating the
// list if there is none, so that a FROM of name gets the image of the
// architecture the stage asks for.
func addToManifestList(store storage.Store, name, imageID string) error {
	listID, list, err := manifests.LoadFromImage(store, name)
	if errors.Is(err, storage.ErrImageUnknown) {
		list, err = manifests.Create(), nil
	}
	if err != nil {
		return err
	}
	ref, err := istorage.Transport.ParseStoreReference(store, imageID)
	if err != nil {
		return err
	}
	if _, err := list.Add(context.TODO(), nil, ref, false); err != nil {
		return err
	}
	_, err = list.SaveToImage(store, listID, []string{name}, "")
	return err
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

	os.Setenv("GIT_CONFIG_COUNT", "2")
	os.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	os.Setenv("GIT_CONFIG_VALUE_0", filepath.Join(dataDir, "githooks"))
	// git fetch ends by detaching `git maintenance run --auto`, which would
	// still be rewriting .git/objects while buildah tars the clone.
	os.Setenv("GIT_CONFIG_KEY_1", "maintenance.auto")
	os.Setenv("GIT_CONFIG_VALUE_1", "false")
	os.Setenv("GIT_CONFIG_GLOBAL", "/dev/null") // keep developer URL rewrites and helpers out of ADD clones
	os.Setenv("GIT_SSL_CAINFO", certFile)       // git ignores SSL_CERT_FILE; trust the proxy CA explicitly
}
