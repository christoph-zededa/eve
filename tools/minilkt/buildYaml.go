package main

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"gopkg.in/yaml.v3"
)

// BuildYAML is the content of a package's build.yml: the fields linuxkit's
// pkglib accepts, with the same keys, defaults and strictness, so a file that
// "linuxkit pkg build" rejects is rejected here too.
type BuildYAML struct {
	Image        string   `yaml:"image"`
	Org          string   `yaml:"org"`
	Tag          string   `yaml:"tag,omitempty"` // text/template over {{.Hash}}
	Dockerfile   string   `yaml:"dockerfile"`
	Arches       []string `yaml:"arches"`
	ExtraSources []string `yaml:"extra-sources"` // "src:dst", src relative to the package dir
	GitRepo      string   `yaml:"gitrepo"`
	Network      bool     `yaml:"network"`
	DisableCache bool     `yaml:"disable-cache"`
	BuildArgs    []string `yaml:"buildArgs,omitempty"`
	Depends      Depends  `yaml:"depends"`
}

// Depends declares docker images a package needs saved into its build context.
type Depends struct {
	DockerImages DockerImageDepends `yaml:"docker-images"`
}

// DockerImageDepends is the "depends: docker-images:" section.
type DockerImageDepends struct {
	TargetDir string   `yaml:"target-dir"`
	Target    string   `yaml:"target"`
	FromFile  string   `yaml:"from-file"`
	List      []string `yaml:"list"`
}

// Device is a device node to create in the container.
type Device struct {
	Path  string `yaml:"path" json:"path"`
	Type  string `yaml:"type" json:"type"`
	Major int64  `yaml:"major" json:"major"`
	Minor int64  `yaml:"minor" json:"minor"`
	Mode  string `yaml:"mode,omitempty" json:"mode,omitempty"`
}

// Runtime is the part of the config processed at runtime, not used to build the OCI spec.
type Runtime struct {
	Cgroups    *[]string      `yaml:"cgroups,omitempty" json:"cgroups,omitempty"`
	Mounts     *[]specs.Mount `yaml:"mounts,omitempty" json:"mounts,omitempty"`
	Mkdir      *[]string      `yaml:"mkdir,omitempty" json:"mkdir,omitempty"`
	Interfaces *[]Interface   `yaml:"interfaces,omitempty" json:"interfaces,omitempty"`
	BindNS     Namespaces     `yaml:"bindNS,omitempty" json:"bindNS,omitempty"`
	Namespace  *string        `yaml:"namespace,omitempty" json:"namespace,omitempty"`
}

// Namespaces configures paths to bind namespaces to.
type Namespaces struct {
	Cgroup *string `yaml:"cgroup,omitempty" json:"cgroup,omitempty"`
	Ipc    *string `yaml:"ipc,omitempty" json:"ipc,omitempty"`
	Mnt    *string `yaml:"mnt,omitempty" json:"mnt,omitempty"`
	Net    *string `yaml:"net,omitempty" json:"net,omitempty"`
	Pid    *string `yaml:"pid,omitempty" json:"pid,omitempty"`
	User   *string `yaml:"user,omitempty" json:"user,omitempty"`
	Uts    *string `yaml:"uts,omitempty" json:"uts,omitempty"`
}

// Interface is the runtime config for a network interface.
type Interface struct {
	Name         string `yaml:"name,omitempty" json:"name,omitempty"`
	Add          string `yaml:"add,omitempty" json:"add,omitempty"`
	Peer         string `yaml:"peer,omitempty" json:"peer,omitempty"`
	CreateInRoot bool   `yaml:"createInRoot" json:"createInRoot"`
}

// newBuildYAML returns the defaults linuxkit's NewPkgInfo starts from.
func newBuildYAML() *BuildYAML {
	return &BuildYAML{
		Org:        "linuxkit",
		Arches:     []string{"amd64", "arm64", "riscv64"},
		Tag:        "{{.Hash}}",
		GitRepo:    "https://github.com/linuxkit/linuxkit",
		Dockerfile: "Dockerfile",
	}
}

func parseBuildYAMLDir(p string) (*BuildYAML, error) {
	bs, err := os.ReadFile(filepath.Join(p, "build.yml"))
	if err != nil {
		return nil, err
	}

	by := &BuildYAML{}
	err = yaml.Unmarshal(bs, by)

	return by, err
}

// ImageName returns the image name without a tag, org/image.
func (b *BuildYAML) ImageName() string {
	return b.Org + "/" + b.Image
}

// RenderTag executes the tag template with {{.Hash}} set to hash.
func (b *BuildYAML) RenderTag(hash string) (string, error) {
	text := b.Tag
	if text == "" {
		text = "{{.Hash}}"
	}
	tmpl, err := template.New("tag").Parse(text)
	if err != nil {
		return "", fmt.Errorf("invalid tag template %q: %w", text, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"Hash": hash}); err != nil {
		return "", fmt.Errorf("tag template %q: %w", text, err)
	}
	return buf.String(), nil
}

// Source is a directory copied into the build context at Dst.
type Source struct {
	Src string // directory on disk
	Dst string // slash-separated path inside the build context
}

// Sources returns the package directory itself, placed at "/", followed by
// the extra-sources entries with relative sources resolved against pkgDir.
func (b *BuildYAML) Sources(pkgDir string) ([]Source, error) {
	sources := []Source{{Src: pkgDir, Dst: "/"}}
	for _, entry := range b.ExtraSources {
		src, dst, ok := strings.Cut(entry, ":")
		if !ok || strings.Contains(dst, ":") {
			return nil, fmt.Errorf("bad extra source %q, want src:dst", entry)
		}
		src = filepath.Clean(src)
		if !filepath.IsAbs(src) {
			src = filepath.Join(pkgDir, src)
		}
		sources = append(sources, Source{Src: src, Dst: path.Clean(dst)})
	}
	return sources, nil
}

// Pkg is a package directory together with its parsed build file and the
// tag linuxkit would give the image built from it.
type Pkg struct {
	Dir      string // absolute package directory
	BuildYML string // absolute path of the build file that was read
	Build    *BuildYAML
	Hash     string // what {{.Hash}} expands to, see PkgHash
	Tag      string // rendered tag template
}

// Ref returns the full image reference, org/image:tag.
func (p *Pkg) Ref() string {
	tag := p.Tag
	if tag == "" {
		tag = "latest"
	}
	return p.Build.ImageName() + ":" + tag
}

// buildArgsOf turns a build.yml's KEY=VALUE list into a map.
func (b *BuildYAML) buildArgs() map[string]string {
	args := map[string]string{}
	for _, kv := range b.BuildArgs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil
		}
		args[k] = v
	}
	return args
}

func (by *BuildYAML) tag(hash string) string {
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
