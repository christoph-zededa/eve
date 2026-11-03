package main

import (
	"testing"

	"go.podman.io/image/v5/pkg/shortnames"
	"go.podman.io/image/v5/pkg/sysregistriesv2"
)

// A short name must resolve to exactly one candidate on docker.io, so buildah
// never prompts for a registry or consults the host's registries.conf.
func TestRegistriesConfResolvesShortNamesToDockerIO(t *testing.T) {
	ctx, err := registriesConf(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := shortnames.Resolve(ctx, "lfedge/eve-rust:1.93.1-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.PullCandidates) != 1 {
		t.Fatalf("got %d pull candidates, want 1: %+v", len(resolved.PullCandidates), resolved.PullCandidates)
	}
	if got, want := resolved.PullCandidates[0].Value.String(), "docker.io/lfedge/eve-rust:1.93.1-1"; got != want {
		t.Errorf("got candidate %q, want %q", got, want)
	}
}

func TestRegistriesConfSearchesDockerIOOnly(t *testing.T) {
	ctx, err := registriesConf(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registries, err := sysregistriesv2.UnqualifiedSearchRegistries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(registries) != 1 || registries[0] != "docker.io" {
		t.Errorf("got search registries %v, want [docker.io]", registries)
	}
}
