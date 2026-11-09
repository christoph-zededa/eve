package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.podman.io/image/v5/docker/reference"
)

func images(t *testing.T, dockerfile string, args map[string]string) []string {
	t.Helper()
	got, err := dockerfileImages(strings.NewReader(dockerfile), args)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDockerfileImagesSingleFrom(t *testing.T) {
	got := images(t, "FROM ubuntu:26.04\nRUN true\n", nil)
	if want := []string{"ubuntu:26.04"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDockerfileImagesSkipsStagesAndScratch(t *testing.T) {
	got := images(t, `
FROM golang:1.25 AS build
RUN go build
FROM scratch AS final
COPY --from=build /a /a
FROM final
COPY --from=0 /b /b
COPY --from=build /c /c
`, nil)
	if want := []string{"golang:1.25"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDockerfileImagesIncludesCopyAndMountSources(t *testing.T) {
	got := images(t, `
FROM alpine:3.22
COPY --from=lfedge/eve-bpftrace:abc /x /x
RUN --mount=type=bind,from=docker.io/library/busybox:1.36,source=/,target=/mnt ls /mnt
RUN --mount=type=cache,target=/root/.cache true
`, nil)
	want := []string{"alpine:3.22", "lfedge/eve-bpftrace:abc", "docker.io/library/busybox:1.36"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

const argDockerfile = `
ARG BASE=ubuntu:26.04
ARG PLATFORM=generic
FROM --platform=${BUILDPLATFORM} ${BASE} AS generic
RUN true
FROM ${PLATFORM} AS build
ARG TOOLS=lfedge/eve-tools:1
COPY --from=${TOOLS} /t /t
`

func TestDockerfileImagesExpandsArgDefaults(t *testing.T) {
	got := images(t, argDockerfile, nil)
	if want := []string{"ubuntu:26.04", "lfedge/eve-tools:1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDockerfileImagesHonorsBuildArgs(t *testing.T) {
	got := images(t, argDockerfile, map[string]string{"BASE": "debian:13", "TOOLS": "lfedge/eve-tools:2"})
	if want := []string{"debian:13", "lfedge/eve-tools:2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDockerfileImagesDeduplicates(t *testing.T) {
	got := images(t, "FROM alpine:3.22 AS a\nFROM alpine:3.22 AS b\nCOPY --from=a /x /x\n", nil)
	if want := []string{"alpine:3.22"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDockerfileImagesFailsOnUnresolvableBase(t *testing.T) {
	if _, err := dockerfileImages(strings.NewReader("ARG BASE\nFROM ${BASE}\n"), nil); err == nil {
		t.Fatal("expected an error for a FROM that resolves to nothing")
	}
}

func TestDockerfileImagesMissingFileFails(t *testing.T) {
	if _, err := dockerfileImagesFromPath(filepath.Join(t.TempDir(), "Dockerfile"), nil); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestSplitImage(t *testing.T) {
	const digest = "sha256:a98704f718abdc31c2e51334e5d258dbaf595b3644aac70519c9fdf716df1bc6"
	for _, tc := range []struct{ ref, org, name, tag string }{
		{"lfedge/eve-alpine:1f744180283ffb4eabcc3862531aeacf1de886b3", "lfedge", "eve-alpine", "1f744180283ffb4eabcc3862531aeacf1de886b3"},
		{"lfedge/eve-alpine:1f744180283ffb4eabcc3862531aeacf1de886b3@" + digest, "lfedge", "eve-alpine", "1f744180283ffb4eabcc3862531aeacf1de886b3@" + digest},
		{"lfedge/eve-alpine@" + digest, "lfedge", "eve-alpine", "@" + digest},
		{"docker.io/lfedge/eve-alpine:dev", "lfedge", "eve-alpine", "dev"},
		{"alpine:3.22", "library", "alpine", "3.22"},
		{"alpine", "library", "alpine", ""},
		{"localhost:5000/eve-alpine:dev", "", "eve-alpine", "dev"},
	} {
		org, name, tag := splitImage(tc.ref)
		if org != tc.org || name != tc.name || tag != tc.tag {
			t.Errorf("splitImage(%q) = %q, %q, %q; want %q, %q, %q", tc.ref, org, name, tag, tc.org, tc.name, tc.tag)
		}
	}
}

func TestSplitImagePanicsOnInvalidReference(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected a panic for an invalid reference")
		}
	}()
	splitImage("UPPERCASE/not-allowed")
}

func TestDockerfileImagesForEVEPackages(t *testing.T) {
	dockerfiles, err := filepath.Glob("../../pkg/*/Dockerfile")
	if err != nil || len(dockerfiles) == 0 {
		t.Skip("not inside an EVE checkout")
	}
	for _, dockerfile := range dockerfiles {
		dir := filepath.Dir(dockerfile)
		var args map[string]string
		if b, err := ReadBuildYAML(filepath.Join(dir, "build.yml")); err == nil {
			args = b.buildArgs()
		}
		got, err := dockerfileImagesFromPath(dockerfile, args)
		if err != nil {
			t.Errorf("%s: %v", dockerfile, err)
			continue
		}
		if len(got) == 0 {
			t.Errorf("%s: no images found", dockerfile)
		}
		for _, image := range got {
			if _, err := reference.ParseNormalizedNamed(image); err != nil {
				t.Errorf("%s: %q is not an image reference: %v", dockerfile, image, err)
			}
		}
	}
}

func TestDockerfileImagesForEVEAlpine(t *testing.T) {
	got, err := dockerfileImagesFromPath("../../pkg/alpine/Dockerfile", nil)
	if err != nil {
		t.Skip("not inside an EVE checkout")
	}
	want := []string{
		"lfedge/eve-alpine:1f744180283ffb4eabcc3862531aeacf1de886b3",
		"lfedge/eve-alpine-base:77cc1495c875b7468dbf4123210abf86a1ffb852-07a6e7df6",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// pkg/fw selects its build stage through ARG PLATFORM, so the stage name must
// never surface as an image, whatever the platform.
func TestDockerfileImagesForEVEFirmwareSelectsStageByArg(t *testing.T) {
	for _, platform := range []string{"generic", "nvidia-jp5"} {
		got, err := dockerfileImagesFromPath("../../pkg/fw/Dockerfile", map[string]string{"PLATFORM": platform})
		if err != nil {
			t.Skip("not inside an EVE checkout")
		}
		if want := []string{"lfedge/eve-alpine:ef20bcb617db5545765f4d7486621cf606e5595e"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("PLATFORM=%s: got %v, want %v", platform, got, want)
		}
	}
}
