package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

var listNow = time.Date(2026, 10, 30, 15, 0, 0, 0, time.UTC)

func TestImageRowsSplitRepositoryAndTag(t *testing.T) {
	images := []imageEntry{{
		ID:      "d26eaf460b0f7b8f558305dda9c36c106def8b29a263d3ef5c8306216d0fe885",
		Names:   []string{"localhost/lfedge/eve-debug:7cdfcd66177403195f23509c6b4eb299f546307f"},
		Created: listNow.Add(-4 * time.Minute),
		Labels: map[string]string{
			"org.opencontainers.image.created":                  "2026-10-30T14:13:07Z",
			"org.opencontainers.image.base.name":                "",
			"org.minilkt.additionalimage.lfedge/eve-alpine:aaa": "lfedge/eve-alpine:bbb",
		},
	}}
	want := []imageRow{{
		Repository: "lfedge/eve-debug",
		Tag:        "7cdfcd66177403195f23509c6b4eb299f546307f",
		ID:         "d26eaf460b0f",
		Created:    "4 minutes ago",
		Labels: []string{
			"org.minilkt.additionalimage.lfedge/eve-alpine:aaa",
			"org.opencontainers.image.base.name",
			"org.opencontainers.image.created",
		},
	}}
	if got := imageRows(images, listNow); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestImageRowsOneRowPerName(t *testing.T) {
	images := []imageEntry{{
		ID:      "9fb9f59f48eb0000000000000000000000000000000000000000000000000000",
		Names:   []string{"localhost/lfedge/eve-alpine:aaa", "localhost/lfedge/eve-alpine:bbb"},
		Created: listNow.Add(-10 * 24 * time.Hour),
	}}
	got := imageRows(images, listNow)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	if got[0].Tag != "aaa" || got[1].Tag != "bbb" {
		t.Fatalf("tags: got %q and %q", got[0].Tag, got[1].Tag)
	}
	if got[0].ID != "9fb9f59f48eb" || got[1].ID != got[0].ID {
		t.Fatalf("ids: got %q and %q", got[0].ID, got[1].ID)
	}
}

func TestImageRowsUnnamedImageHasNoRepositoryOrTag(t *testing.T) {
	images := []imageEntry{{ID: "d98097580d9d0000000000000000000000000000000000000000000000000000", Created: listNow}}
	got := imageRows(images, listNow)
	if len(got) != 1 || got[0].Repository != noneValue || got[0].Tag != noneValue {
		t.Fatalf("got %+v", got)
	}
}

func TestImageRowsDigestOnlyNameShowsDigestAsTag(t *testing.T) {
	const digest = "sha256:53bfcece9d3c9f65be7b9ed163c713597fd649f9b4f88a5ab19c61409b2a589d"
	images := []imageEntry{{
		ID:      "a1953b09b2660000000000000000000000000000000000000000000000000000",
		Names:   []string{"docker.io/lfedge/eve-alpine@" + digest},
		Created: listNow,
	}}
	got := imageRows(images, listNow)
	if len(got) != 1 || got[0].Repository != "lfedge/eve-alpine" || got[0].Tag != "@"+digest {
		t.Fatalf("got %+v", got)
	}
}

func TestImageRowsNewestLast(t *testing.T) {
	images := []imageEntry{
		{ID: "1111111111111111", Names: []string{"lfedge/new:1"}, Created: listNow.Add(-time.Hour)},
		{ID: "0000000000000000", Names: []string{"lfedge/old:1"}, Created: listNow.Add(-48 * time.Hour)},
	}
	got := imageRows(images, listNow)
	if got[0].Repository != "lfedge/old" || got[1].Repository != "lfedge/new" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Created != "2 days ago" || got[1].Created != "About an hour ago" {
		t.Fatalf("created: got %q and %q", got[0].Created, got[1].Created)
	}
}

func TestImageRowsWithoutLabelsHaveNone(t *testing.T) {
	images := []imageEntry{{ID: "0000000000000000", Names: []string{"localhost/x:1"}, Created: listNow}}
	if got := imageRows(images, listNow); len(got[0].Labels) != 0 {
		t.Fatalf("got labels %q, want none", got[0].Labels)
	}
}

// Only minilkt labels are listed, as bullets; unnamed images are left out.
func TestRenderImagesListsMinilktLabelsAsBulletsAndSkipsUnnamed(t *testing.T) {
	rows := []imageRow{
		{Repository: "localhost/lfedge/eve-debug", Tag: "7cdfcd66", ID: "d26eaf460b0f", Created: "4 minutes ago",
			Labels: []string{
				"org.minilkt.additionalimage.lfedge/eve-alpine:1111.lfedge/eve-alpine:ffb8",
				"org.opencontainers.image.created",
			}},
		{Repository: noneValue, Tag: noneValue, ID: "d98097580d9d", Created: "4 minutes ago"},
	}
	var buf bytes.Buffer
	if err := renderImages(&buf, rows); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	want := [][]string{
		{"REPOSITORY", "TAG", "IMAGE", "ID", "CREATED", "MINILKT", "LABELS"},
		{"localhost/lfedge/eve-debug", "7cdfcd66", "d26eaf460b0f", "4", "minutes", "ago"},
		{"-", "org.minilkt.additionalimage.lfedge/eve-alpine:1111.lfedge/eve-alpine:ffb8"},
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), buf.String())
	}
	for i, line := range lines {
		if got := strings.Fields(line); !reflect.DeepEqual(got, want[i]) {
			t.Fatalf("line %d: got %v, want %v", i, got, want[i])
		}
	}
}

const testConfigDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestManifestConfigDigestForDockerAndOCIManifests(t *testing.T) {
	docker := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json",` +
		`"config":{"mediaType":"application/vnd.docker.container.image.v1+json","digest":"` + testConfigDigest + `","size":1},"layers":[]}`)
	oci := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
		`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + testConfigDigest + `","size":1},"layers":[]}`)
	for name, blob := range map[string][]byte{"docker": docker, "oci": oci} {
		if got, ok := manifestConfigDigest(blob); !ok || got != testConfigDigest {
			t.Fatalf("%s: got %q, %v", name, got, ok)
		}
	}
	if got, ok := manifestConfigDigest([]byte("not a manifest")); ok {
		t.Fatalf("garbage: got %q, want none", got)
	}
}

func TestConfigLabels(t *testing.T) {
	blob := []byte(`{"architecture":"amd64","os":"linux","config":{"Env":["PATH=/bin"],` +
		`"Labels":{"io.buildah.version":"1.45.0","org.minilkt.additionalimage.lfedge/eve-alpine-base:77cc":""}}}`)
	want := map[string]string{"io.buildah.version": "1.45.0", "org.minilkt.additionalimage.lfedge/eve-alpine-base:77cc": ""}
	if got := configLabels(blob); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := configLabels([]byte(`{"config":{}}`)); len(got) != 0 {
		t.Fatalf("no labels: got %v", got)
	}
}
