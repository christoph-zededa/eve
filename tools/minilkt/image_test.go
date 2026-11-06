package main

import (
	"testing"
)

// podman push defaults a bare destination name to :latest, so uploadImage
// must address the registry the same way.
func TestPushReferenceDefaultsToLatest(t *testing.T) {
	ref, err := pushReference("lfedge", "eve-alpine")
	if err != nil {
		t.Fatal(err)
	}
	if got := ref.Transport().Name(); got != "docker" {
		t.Errorf("got transport %q, want docker", got)
	}
	if got, want := ref.DockerReference().String(), "docker.io/lfedge/eve-alpine:latest"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPushReferenceKeepsTag(t *testing.T) {
	ref, err := pushReference("lfedge", "eve-alpine:1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ref.DockerReference().String(), "docker.io/lfedge/eve-alpine:1.2.3"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPushReferenceRejectsInvalidName(t *testing.T) {
	if _, err := pushReference("lfedge", "Eve-Alpine"); err == nil {
		t.Error("got nil error for an uppercase repository name")
	}
}
