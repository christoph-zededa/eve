package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseBuildYAMLAppliesLinuxkitDefaults(t *testing.T) {
	b, err := ParseBuildYAML(strings.NewReader("image: probe\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := &BuildYAML{
		Image:      "probe",
		Org:        "linuxkit",
		Tag:        "{{.Hash}}",
		Dockerfile: "Dockerfile",
		Arches:     []string{"amd64", "arm64", "riscv64"},
		GitRepo:    "https://github.com/linuxkit/linuxkit",
	}
	if !reflect.DeepEqual(b, want) {
		t.Fatalf("got %+v, want %+v", b, want)
	}
	if b.ImageName() != "linuxkit/probe" {
		t.Fatalf("ImageName() = %q, want linuxkit/probe", b.ImageName())
	}
}

func TestParseBuildYAMLRequiresImage(t *testing.T) {
	if _, err := ParseBuildYAML(strings.NewReader("org: lfedge\n")); err == nil {
		t.Fatal("expected an error for a build.yml without image")
	}
}

func TestParseBuildYAMLRejectsUnknownFields(t *testing.T) {
	for _, doc := range []string{
		"image: probe\nimag: typo\n",
		"image: probe\nconfig:\n  bindz: []\n",
		"image: probe\nconfig:\n  devices:\n    - path: all\n      typ: a\n",
	} {
		if _, err := ParseBuildYAML(strings.NewReader(doc)); err == nil {
			t.Errorf("expected an error for unknown field in %q", doc)
		}
	}
}

const eveStyleBuildYAML = `# linuxkit build template
#
# SPDX-License-Identifier: Apache-2.0
---
org: lfedge
image: eve-pillar
tag: "{{.Hash}}-kube"
network: yes
disable-cache: true
extra-sources:
  - ../../extra:/extra
config:
  binds:
    - /dev:/dev # devices
    - /persist:/persist:rshared,rbind
  net: host
  pid: host
  userns: host
  uid: 101
  additionalGids: [100]
  oomScoreAdj: -800
  readonly: true
  rootfsPropagation: shared
  capabilities:
    - all
  devices:
    - path: all
      type: a
    - path: "/dev/tty"
      type: c
      major: 5
      minor: 0
      mode: 0666
buildArgs:
  - HV=k
  - DEV=y
`

func TestParseBuildYAMLReadsEveStyleFile(t *testing.T) {
	b, err := ParseBuildYAML(strings.NewReader(eveStyleBuildYAML))
	if err != nil {
		t.Fatal(err)
	}
	if b.Org != "lfedge" || b.Image != "eve-pillar" || b.Tag != "{{.Hash}}-kube" {
		t.Fatalf("org/image/tag = %q/%q/%q", b.Org, b.Image, b.Tag)
	}
	if !b.Network || !b.DisableCache {
		t.Fatalf("network = %v, disable-cache = %v, want both true", b.Network, b.DisableCache)
	}
	if !reflect.DeepEqual(b.ExtraSources, []string{"../../extra:/extra"}) {
		t.Fatalf("extra-sources = %v", b.ExtraSources)
	}
	if !reflect.DeepEqual(b.BuildArgs, []string{"HV=k", "DEV=y"}) {
		t.Fatalf("buildArgs = %v", b.BuildArgs)
	}
	c := b.Config
	if c == nil {
		t.Fatal("config not parsed")
	}
	if !reflect.DeepEqual(*c.Binds, []string{"/dev:/dev", "/persist:/persist:rshared,rbind"}) {
		t.Fatalf("binds = %v", *c.Binds)
	}
	if c.Net != "host" || c.Pid != "host" || c.Userns != "host" {
		t.Fatalf("net/pid/userns = %q/%q/%q", c.Net, c.Pid, c.Userns)
	}
	if *c.UID != 101 || !reflect.DeepEqual(*c.AdditionalGids, []interface{}{100}) {
		t.Fatalf("uid = %v, additionalGids = %v", *c.UID, *c.AdditionalGids)
	}
	if *c.OOMScoreAdj != -800 || !*c.Readonly || *c.RootfsPropagation != "shared" {
		t.Fatalf("oomScoreAdj = %d, readonly = %v, rootfsPropagation = %q", *c.OOMScoreAdj, *c.Readonly, *c.RootfsPropagation)
	}
	if !reflect.DeepEqual(*c.Capabilities, []string{"all"}) {
		t.Fatalf("capabilities = %v", *c.Capabilities)
	}
	wantDevices := []Device{
		{Path: "all", Type: "a"},
		{Path: "/dev/tty", Type: "c", Major: 5, Minor: 0, Mode: "0666"},
	}
	if !reflect.DeepEqual(*c.Devices, wantDevices) {
		t.Fatalf("devices = %+v, want %+v", *c.Devices, wantDevices)
	}
}

// The config section ends up verbatim as the org.mobyproject.config label of
// the built image, so it has to serialize exactly like linuxkit's struct does.
func TestImageConfigMarshalsLikeTheMobyLabel(t *testing.T) {
	b, err := ParseBuildYAML(strings.NewReader(eveStyleBuildYAML))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(*b.Config)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"capabilities":["all"],` +
		`"binds":["/dev:/dev","/persist:/persist:rshared,rbind"],` +
		`"devices":[{"path":"all","type":"a","major":0,"minor":0},` +
		`{"path":"/dev/tty","type":"c","major":5,"minor":0,"mode":"0666"}],` +
		`"net":"host","pid":"host","userns":"host","readonly":true,` +
		`"uid":101,"additionalGids":[100],"oomScoreAdj":-800,"rootfsPropagation":"shared"}`
	if string(got) != want {
		t.Fatalf("label JSON\n got %s\nwant %s", got, want)
	}
}

func TestRenderTagSubstitutesHash(t *testing.T) {
	for _, tc := range []struct{ tmpl, want string }{
		{"{{.Hash}}", "abc123"},
		{"{{.Hash}}-nvidia-jp5", "abc123-nvidia-jp5"},
		{"", "abc123"},
		{"fixed", "fixed"},
	} {
		b := &BuildYAML{Tag: tc.tmpl}
		got, err := b.RenderTag("abc123")
		if err != nil {
			t.Errorf("RenderTag(%q): %v", tc.tmpl, err)
		} else if got != tc.want {
			t.Errorf("RenderTag(%q) = %q, want %q", tc.tmpl, got, tc.want)
		}
	}
	if _, err := (&BuildYAML{Tag: "{{.Hash"}).RenderTag("abc123"); err == nil {
		t.Error("expected an error for an unterminated template")
	}
}

func TestSourcesResolveExtraSourcesAgainstPackageDir(t *testing.T) {
	b := &BuildYAML{ExtraSources: []string{"../../extra/one:/one", "/abs/two:/two/"}}
	got, err := b.Sources("/repo/pkg/probe")
	if err != nil {
		t.Fatal(err)
	}
	want := []Source{
		{Src: "/repo/pkg/probe", Dst: "/"},
		{Src: "/repo/extra/one", Dst: "/one"},
		{Src: "/abs/two", Dst: "/two"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Sources() = %+v, want %+v", got, want)
	}
	for _, bad := range []string{"nocolon", "a:b:c"} {
		if _, err := (&BuildYAML{ExtraSources: []string{bad}}).Sources("/repo/pkg/probe"); err == nil {
			t.Errorf("expected an error for extra source %q", bad)
		}
	}
}

func TestLoadPkgRendersLinuxkitReference(t *testing.T) {
	f := newFixture(t)
	f.write("pkg/probe/build.yml", "image: probe\norg: acme\ntag: \"{{.Hash}}-x\"\n")
	f.commit()
	p, err := LoadPkg(f.pkg, "build.yml")
	if err != nil {
		t.Fatal(err)
	}
	tree := f.git("rev-parse", "HEAD:pkg/probe")
	if p.Hash != tree {
		t.Fatalf("Hash = %q, want %q", p.Hash, tree)
	}
	if want := "acme/probe:" + tree + "-x"; p.Ref() != want {
		t.Fatalf("Ref() = %q, want %q", p.Ref(), want)
	}
	if p.Dir != f.pkg || p.BuildYML != filepath.Join(f.pkg, "build.yml") {
		t.Fatalf("Dir = %q, BuildYML = %q", p.Dir, p.BuildYML)
	}
}

func TestLoadPkgDefaultsToBuildYML(t *testing.T) {
	f := newFixture(t)
	p, err := LoadPkg(f.pkg, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.BuildYML != filepath.Join(f.pkg, "build.yml") {
		t.Fatalf("BuildYML = %q", p.BuildYML)
	}
}

func TestLoadPkgFoldsExtraSourcesIntoHash(t *testing.T) {
	f := newFixture(t)
	f.write("extra/one/x", "x\n")
	f.write("pkg/probe/build.yml", "image: probe\nextra-sources:\n  - ../../extra/one:/one\n")
	f.commit()
	p, err := LoadPkg(f.pkg, "build.yml")
	if err != nil {
		t.Fatal(err)
	}
	want, err := PkgHash(f.pkg, filepath.Join(f.root, "extra", "one"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Hash != want {
		t.Fatalf("Hash = %q, want %q", p.Hash, want)
	}
}

func TestLoadPkgMissingBuildYMLFails(t *testing.T) {
	f := newFixture(t)
	if _, err := LoadPkg(f.pkg, "build-nope.yml"); err == nil {
		t.Fatal("expected an error for a missing build file")
	}
}

// eveBuildFiles returns every build*.yml of the enclosing EVE checkout, or
// skips the test outside one.
func eveBuildFiles(t *testing.T) []string {
	t.Helper()
	ymls, err := filepath.Glob("../../pkg/*/build*.yml")
	if err != nil || len(ymls) == 0 {
		t.Skip("not inside an EVE checkout")
	}
	return ymls
}

func TestParseBuildYAMLAcceptsEveBuildFiles(t *testing.T) {
	for _, yml := range eveBuildFiles(t) {
		b, err := ReadBuildYAML(yml)
		if err != nil {
			t.Errorf("%s: %v", yml, err)
			continue
		}
		if b.Org != "lfedge" || !strings.HasPrefix(b.Image, "eve") {
			t.Errorf("%s: parsed as %s", yml, b.ImageName())
		}
	}
}

// TestLoadPkgMatchesLinuxkitForEVEBuildFiles compares the full image
// reference against the real build tool for every build*.yml of the
// enclosing EVE checkout, platform and hypervisor variants included.
func TestLoadPkgMatchesLinuxkitForEVEBuildFiles(t *testing.T) {
	for _, yml := range eveBuildFiles(t) {
		dir, err := filepath.Abs(filepath.Dir(yml))
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(yml)
		p, err := LoadPkg(dir, name)
		if err != nil {
			t.Errorf("%s: %v", yml, err)
			continue
		}
		if want := linuxkitShowTag(t, dir, name); p.Ref() != want {
			t.Errorf("%s: ref %q, linuxkit says %q", yml, p.Ref(), want)
		}
	}
}

// linuxkitShowTag runs the real linuxkit binary as the reference and returns
// the full image reference it prints for the given build file.
func linuxkitShowTag(t *testing.T, pkgDir, buildYML string) string {
	t.Helper()
	out, err := exec.Command(linuxkitBin(t), "pkg", "show-tag", "--build-yml", buildYML, pkgDir).CombinedOutput()
	if err != nil {
		t.Fatalf("linuxkit pkg show-tag --build-yml %s %s: %v\n%s", buildYML, pkgDir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// linuxkitBin returns the linuxkit binary to use as reference, preferring the
// one EVE builds under build-tools, or skips the test when there is none.
func linuxkitBin(t *testing.T) string {
	t.Helper()
	bin, err := filepath.Abs("../../build-tools/bin/linuxkit")
	if err != nil || !isExecutable(bin) {
		if bin, err = exec.LookPath("linuxkit"); err != nil {
			t.Skip("no linuxkit binary available as reference")
		}
	}
	return bin
}
