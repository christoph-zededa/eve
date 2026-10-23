package main

import (
	"bytes"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestBuildOrderPutsDependenciesFirst(t *testing.T) {
	deps := map[string][]string{
		"lfedge/eve-pillar":          {"lfedge/eve-alpine:abc", "lfedge/eve-cross-compilers:def"},
		"lfedge/eve-alpine":          {"lfedge/eve-alpine-base:123", "docker.io/library/golang:1.25"},
		"lfedge/eve-alpine-base":     {"alpine:3.22"},
		"lfedge/eve-cross-compilers": {"lfedge/eve-alpine:abc"},
	}
	got, err := buildOrder(deps)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lfedge/eve-alpine-base", "lfedge/eve-alpine", "lfedge/eve-cross-compilers", "lfedge/eve-pillar"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestBuildOrderIsAlphabeticalAmongUnrelatedImages(t *testing.T) {
	deps := map[string][]string{"c": nil, "a": nil, "b": {"ubuntu:26.04"}}
	for range 5 {
		got, err := buildOrder(deps)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestBuildOrderMatchesDependenciesByNameOnly(t *testing.T) {
	deps := map[string][]string{
		"lfedge/eve-alpine": nil,
		"lfedge/eve-pillar": {"docker.io/lfedge/eve-alpine:abc@sha256:0123456789012345678901234567890123456789012345678901234567890123"},
	}
	got, err := buildOrder(deps)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"lfedge/eve-alpine", "lfedge/eve-pillar"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// pkg/alpine builds from an older published lfedge/eve-alpine. Using an
// earlier image of itself is not a cycle.
func TestBuildOrderIgnoresSelfDependency(t *testing.T) {
	deps := map[string][]string{
		"lfedge/eve-alpine":      {"lfedge/eve-alpine:old", "lfedge/eve-alpine-base:x"},
		"lfedge/eve-alpine-base": nil,
	}
	got, err := buildOrder(deps)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"lfedge/eve-alpine-base", "lfedge/eve-alpine"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestBuildOrderReportsCycles(t *testing.T) {
	deps := map[string][]string{
		"a": {"b:1"},
		"b": {"c:1"},
		"c": {"a:1"},
		"d": nil,
	}
	_, err := buildOrder(deps)
	if err == nil {
		t.Fatal("expected a cycle error")
	}
	for _, name := range []string{"a", "b", "c"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}

func TestDumpBuildMultiWritesImagesInBuildOrder(t *testing.T) {
	deps := map[string][]string{
		"lfedge/eve-pillar":      {"lfedge/eve-alpine:abc"},
		"lfedge/eve-alpine":      {"lfedge/eve-alpine-base:123", "golang:1.25"},
		"lfedge/eve-alpine-base": {"alpine:3.22"},
	}
	var out bytes.Buffer
	if err := dumpBuildMulti(&out, deps); err != nil {
		t.Fatal(err)
	}
	want := "lfedge/eve-alpine-base\n" +
		"lfedge/eve-alpine <- lfedge/eve-alpine-base\n" +
		"lfedge/eve-pillar <- lfedge/eve-alpine\n"
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
}

func TestBuildOrderForEVEPackages(t *testing.T) {
	ymls, err := filepath.Glob("../../pkg/*/build.yml")
	if err != nil || len(ymls) == 0 {
		t.Skip("not inside an EVE checkout")
	}
	deps := map[string][]string{}
	for _, yml := range ymls {
		dir := filepath.Dir(yml)
		b, err := ReadBuildYAML(yml)
		if err != nil {
			t.Fatal(err)
		}
		images, err := DockerfileImages(filepath.Join(dir, "Dockerfile"), nil)
		if err != nil {
			t.Fatal(err)
		}
		deps[b.ImageName()] = images
	}
	order, err := buildOrder(deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != len(deps) {
		t.Fatalf("%d images ordered, %d packages", len(order), len(deps))
	}
	pos := func(name string) int {
		i := slices.Index(order, name)
		if i < 0 {
			t.Fatalf("%s missing from the order", name)
		}
		return i
	}
	if !(pos("lfedge/eve-alpine-base") < pos("lfedge/eve-alpine") && pos("lfedge/eve-alpine") < pos("lfedge/eve-pillar")) {
		t.Fatalf("unexpected order: %v", order)
	}
}
