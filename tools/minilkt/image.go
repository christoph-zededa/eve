package main

import (
	"context"
	"fmt"
	"os"

	"go.podman.io/buildah"
	"go.podman.io/image/v5/docker"
	"go.podman.io/image/v5/types"
	"go.podman.io/storage/pkg/unshare"
)

// uploadImage pushes the image ID from the minilkt store to
// docker.io/org/name, like `podman push ID docker.io/org/name`. Credentials
// come from the places podman looks: the containers auth.json files and
// ~/.docker/config.json.
func uploadImage(ID, org, name string) error {
	unshare.MaybeReexecUsingUserNamespace(false)

	dest, err := pushReference(org, name)
	if err != nil {
		return err
	}

	store, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _, _ = store.Shutdown(false) }()

	sysCtx, err := registriesConf()
	if err != nil {
		return err
	}

	_, manifestDigest, err := buildah.Push(context.TODO(), ID, dest, buildah.PushOptions{
		Store:         store,
		SystemContext: sysCtx,
		ReportWriter:  os.Stdout,
	})
	if err != nil {
		return fmt.Errorf("pushing %s to %s failed: %w", ID, dest.DockerReference(), err)
	}
	fmt.Printf("pushed %s (%s)\n", dest.DockerReference(), manifestDigest)
	return nil
}

func downloadImage(org, name string) error {
	unshare.MaybeReexecUsingUserNamespace(false)

	dest, err := pushReference(org, name)
	if err != nil {
		return err
	}

	store, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _, _ = store.Shutdown(false) }()

	sysCtx, err := registriesConf()
	if err != nil {
		return err
	}

	imageName := dest.DockerReference().String()
	manifestDigest, err := buildah.Pull(context.Background(), imageName, buildah.PullOptions{
		PullPolicy:    buildah.PullIfNewer,
		Store:         store,
		SystemContext: sysCtx,
		ReportWriter:  os.Stdout,
	})

	fmt.Printf("pulled %s\n", manifestDigest)

	return nil
}

// pushReference returns the registry reference for docker.io/org/name; a name
// without a tag gets :latest, as podman push does.
func pushReference(org, name string) (types.ImageReference, error) {
	return docker.ParseReference("//docker.io/" + org + "/" + name)
}
