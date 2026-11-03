package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	units "github.com/docker/go-units"
	"go.podman.io/image/v5/manifest"
	"go.podman.io/storage"
	"go.podman.io/storage/pkg/unshare"
)

const noneValue = ""

// imageEntry is the store-independent view of an image used for listing.
type imageEntry struct {
	ID      string
	Names   []string
	Created time.Time
	Labels  map[string]string // image config labels, present in both formats
}

type imageRow struct {
	Repository, Tag, ID, Created string
	Labels                       []string // annotation and label keys, sorted
}

// imageRows turns images into one row per name, oldest image first so the
// newest builds end up at the bottom. Unnamed images get a single <none> row.
func imageRows(images []imageEntry, now time.Time) []imageRow {
	images = slices.Clone(images)
	slices.SortStableFunc(images, func(a, b imageEntry) int {
		return a.Created.Compare(b.Created)
	})
	var rows []imageRow
	for _, img := range images {
		row := imageRow{
			ID:      shortID(img.ID),
			Created: units.HumanDuration(now.Sub(img.Created)) + " ago",
			Labels:  annotationList(img.Labels, img.Labels),
		}
		names := img.Names
		if len(names) == 0 {
			names = []string{""}
		}
		for _, name := range names {
			row.Repository, row.Tag = splitRepoTag(name)
			rows = append(rows, row)
		}
	}
	return rows
}

// annotationList returns the keys of the given annotation or label sets,
// sorted and without duplicates.
func annotationList(sets ...map[string]string) []string {
	keys := map[string]struct{}{}
	for _, set := range sets {
		for k := range set {
			keys[k] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(keys))
}

// splitRepoTag maps a stored image name to the linuxkit-style repository
// (org/name without the registry) and tag used in the listing; an unnamed
// image yields noneValue for both.
func splitRepoTag(name string) (repo, tag string) {
	if name == "" {
		return noneValue, noneValue
	}
	org, image, tag := splitImage(name)
	if org == "" {
		return image, tag
	}
	return org + "/" + image, tag
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// renderImages prints one line per row; further annotations of a row
// continue on their own lines in the last column.
func renderImages(w io.Writer, rows []imageRow) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REPOSITORY\tTAG\tIMAGE ID\tCREATED\tMINILKT LABELS")
	for _, r := range rows {
		if r.Tag == noneValue || r.Repository == noneValue {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Repository, r.Tag, r.ID, r.Created)
		for _, tag := range r.Labels {
			if !strings.HasPrefix(tag, "org.minilkt.additionalimage") {
				continue
			}
			fmt.Fprintf(tw, "\t- %s\n", tag)
		}
	}
	return tw.Flush()
}

// listImages prints the images in the store that build writes to.
func listImages(w io.Writer) error {
	unshare.MaybeReexecUsingUserNamespace(false)

	store, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _, _ = store.Shutdown(false) }()

	images, err := store.Images()
	if err != nil {
		return err
	}
	entries := make([]imageEntry, 0, len(images))
	for _, img := range images {
		entries = append(entries, imageEntry{
			ID:      img.ID,
			Names:   img.Names,
			Created: img.Created,
			Labels:  imageLabels(store, img.ID),
		})
	}
	return renderImages(w, imageRows(entries, time.Now()))
}

// imageLabels reads the labels of the image's config, which both the OCI and
// the Docker image formats carry; podman inspect reports them as Labels.
func imageLabels(store storage.Store, id string) map[string]string {
	blob, err := store.ImageBigData(id, storage.ImageDigestBigDataKey)
	if err != nil {
		return nil
	}
	configDigest, ok := manifestConfigDigest(blob)
	if !ok {
		return nil
	}
	config, err := store.ImageBigData(id, configDigest)
	if err != nil {
		return nil
	}
	return configLabels(config)
}

// manifestConfigDigest returns the digest of the config blob an image
// manifest of either format points to.
func manifestConfigDigest(blob []byte) (string, bool) {
	m, err := manifest.FromBlob(blob, manifest.GuessMIMEType(blob))
	if err != nil {
		return "", false
	}
	return m.ConfigInfo().Digest.String(), true
}

func configLabels(blob []byte) map[string]string {
	var config struct {
		Config struct {
			Labels map[string]string
		}
	}
	if err := json.Unmarshal(blob, &config); err != nil {
		return nil
	}
	return config.Config.Labels
}
