package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	docker "github.com/fsouza/go-dockerclient"
	"github.com/openshift/imagebuilder"
	"github.com/openshift/imagebuilder/dockerfile/parser"
	"go.podman.io/image/v5/docker/reference"
)

func parse(dockerfilePath string) {
	bs, err := os.ReadFile(dockerfilePath)
	if err != nil {
		panic(err)
	}
	mainNode, err := imagebuilder.ParseDockerfile(bytes.NewReader(bs))
	// fmt.Printf("main node: %s\n", mainNode.Dump())
	dump(mainNode, 0)
}

func dump(n *parser.Node, depth int) {
	for i := 0; i < depth; i++ {
		fmt.Printf(" ")
	}

	fmt.Printf("val: %s\n", n.Value)

	for _, c := range n.Children {
		dump(c, depth+20)
		fmt.Println("-------------")
	}

	next := n.Next
	for next != nil {
		dump(next, depth+1)
		next = next.Next
	}

}

// DockerfileImages returns every image the Dockerfile at path would pull, in
// order of first mention and without repeats: the base of each stage, and the
// sources of COPY --from and RUN --mount=from=... that do not name a stage.
// ARG references in those names are resolved from buildArgs and the file's
// own defaults, as a build would. scratch is not an image and is left out.
func DockerfileImages(path string, buildArgs map[string]string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	images, err := dockerfileImages(bytes.NewReader(data), buildArgs)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return images, nil
}

func dockerfileImages(r io.Reader, buildArgs map[string]string) ([]string, error) {
	node, err := imagebuilder.ParseDockerfile(r)
	if err != nil {
		return nil, err
	}
	stages, err := imagebuilder.NewStages(node, imagebuilder.NewBuilder(buildArgs))
	if err != nil {
		return nil, err
	}
	var images []string
	seen := map[string]struct{}{}
	add := func(name string) {
		if _, dup := seen[name]; !dup {
			seen[name] = struct{}{}
			images = append(images, name)
		}
	}
	for _, stage := range stages {
		base, err := stage.Builder.From(stage.Node)
		if err != nil {
			return nil, fmt.Errorf("stage %d: %w", stage.Position, err)
		}
		if base == "" {
			return nil, fmt.Errorf("stage %d: FROM resolves to an empty name", stage.Position)
		}
		if base != "scratch" && !isStage(stages, base, stage.Position) {
			add(base)
		}
		// Running the stage's instructions through a recording executor
		// resolves ARG and ENV the way a build does before the names in
		// COPY --from and RUN --mount are looked at.
		refs := &imageRefs{Executor: imagebuilder.NoopExecutor}
		for _, child := range stage.Node.Children {
			step := stage.Builder.Step()
			if err := step.Resolve(child); err != nil {
				return nil, fmt.Errorf("stage %d: %w", stage.Position, err)
			}
			if err := stage.Builder.Run(step, refs, false); err != nil {
				return nil, fmt.Errorf("stage %d: %w", stage.Position, err)
			}
		}
		for _, name := range refs.names {
			if !isStage(stages, name, stage.Position) {
				add(name)
			}
		}
	}
	return images, nil
}

// isStage reports whether name refers to one of the stages before position,
// by its alias or by its index.
func isStage(stages imagebuilder.Stages, name string, position int) bool {
	for _, s := range stages[:position] {
		if s.Name == name {
			return true
		}
	}
	index, err := strconv.Atoi(name)
	return err == nil && index >= 0 && index < position
}

// imageRefs is an imagebuilder.Executor that only notes where COPY --from
// and RUN --mount=from=... read from.
type imageRefs struct {
	imagebuilder.Executor
	names []string
}

func (c *imageRefs) Copy(_ []string, copies ...imagebuilder.Copy) error {
	for _, cp := range copies {
		if cp.From != "" {
			c.names = append(c.names, cp.From)
		}
	}
	return nil
}

func (c *imageRefs) Run(run imagebuilder.Run, _ docker.Config) error {
	for _, mount := range run.Mounts {
		for _, kv := range strings.Split(mount, ",") {
			if from, ok := strings.CutPrefix(kv, "from="); ok && from != "" {
				c.names = append(c.names, from)
			}
		}
	}
	return nil
}

// splitImage takes a reference apart: the path's leading components ("lfedge",
// "library" for official images, "" for a registry-local name), its last
// component, and everything after the name as one string: "tag",
// "tag@sha256:…" or "@sha256:…". It panics on an invalid reference.
func splitImage(ref string) (org, name, tag string) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		panic(err)
	}
	path := reference.Path(named)
	if i := strings.LastIndex(path, "/"); i >= 0 {
		org, name = path[:i], path[i+1:]
	} else {
		name = path
	}
	if tagged, ok := named.(reference.Tagged); ok {
		tag = tagged.Tag()
	}
	if digested, ok := named.(reference.Digested); ok {
		tag += "@" + digested.Digest().String()
	}
	return org, name, tag
}
