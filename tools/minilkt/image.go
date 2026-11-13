package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.podman.io/buildah"
	"go.podman.io/buildah/define"
	"go.podman.io/image/v5/docker"
	"go.podman.io/image/v5/manifest"
	istorage "go.podman.io/image/v5/storage"
	"go.podman.io/image/v5/types"
	"go.podman.io/storage"
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

// addImageAnnotations returns the ID of an image that is imageID plus the
// given labels, each "key=value" or "key" as in BuildOptions.Labels. Labels
// are part of the image config, which the ID is derived from, so they cannot
// be added in place: a copy on the same layers, with the same history and
// creation date, is committed and takes over the names of imageID. imageID is
// removed unless a container still uses it. An image that already carries all
// the labels is returned as it is.
func addImageAnnotations(store storage.Store, sysCtx *types.SystemContext, imageID string, annotations []string) (string, error) {
	img, err := store.Image(imageID)
	if err != nil {
		return "", err
	}
	if len(img.Names) == 0 {
		return "", fmt.Errorf("image %s has no name the labelled copy could take over", img.ID)
	}

	have := imageLabels(store, img.ID)
	missing := map[string]string{}
	for _, annotation := range annotations {
		key, value, _ := strings.Cut(annotation, "=")
		if old, ok := have[key]; key != "" && (!ok || old != value) {
			missing[key] = value
		}
	}
	if len(missing) == 0 {
		return img.ID, nil
	}

	dest, err := istorage.Transport.ParseStoreReference(store, img.Names[0])
	if err != nil {
		return "", err
	}
	format := define.OCIv1ImageManifest
	if blob, err := store.ImageBigData(img.ID, storage.ImageDigestBigDataKey); err == nil && manifest.GuessMIMEType(blob) == define.Dockerv2ImageManifest {
		format = define.Dockerv2ImageManifest
	}

	builder, err := buildah.NewBuilder(context.TODO(), store, buildah.BuilderOptions{
		FromImage:             img.ID,
		PullPolicy:            define.PullNever,
		SystemContext:         sysCtx,
		PreserveBaseImageAnns: true,
	})
	if err != nil {
		return "", fmt.Errorf("creating a working container from %s failed: %w", img.ID, err)
	}
	for key, value := range missing {
		builder.SetLabel(key, value)
	}
	// No history entry and the old creation date: the layer cache of builds
	// based on this image stays valid and the new ID only depends on the labels.
	newID, _, _, err := builder.Commit(context.TODO(), dest, buildah.CommitOptions{
		PreferredManifestType: format,
		SystemContext:         sysCtx,
		OmitLayerHistoryEntry: true,
		SourceDateEpoch:       &img.Created,
	})
	// The working container pins img, so it has to go before img can.
	if errDelete := builder.Delete(); err == nil {
		err = errDelete
	}
	if err != nil {
		return "", fmt.Errorf("adding labels to %s failed: %w", img.Names[0], err)
	}
	if newID == img.ID {
		return newID, nil
	}
	if len(img.Names) > 1 {
		if err := store.AddNames(newID, img.Names[1:]); err != nil {
			return "", err
		}
	}
	if _, err := store.DeleteImage(img.ID, true); err != nil && !errors.Is(err, storage.ErrImageUsedByContainer) {
		return "", fmt.Errorf("removing unlabelled image %s failed: %w", img.ID, err)
	}
	return newID, nil
}
