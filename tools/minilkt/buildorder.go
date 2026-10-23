package main

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"go.podman.io/image/v5/docker/reference"
)

// buildOrder sorts the keys of deps so that every image comes after the
// images it depends on. Keys are package images such as "lfedge/eve-alpine";
// values are references as written in Dockerfiles. A reference is matched to
// a key by name, ignoring registry prefix, tag and digest, so
// "lfedge/eve-alpine:<hash>" depends on the "lfedge/eve-alpine" package.
// References to images that are not keys are external and impose no order,
// and a package building from an older image of itself is not a cycle.
// Among unordered images the alphabetical order wins, so the result is
// stable.
func buildOrder(deps map[string][]string) ([]string, error) {
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var order []string
	var visit func(key string, path []string) error
	visit = func(key string, path []string) error {
		switch state[key] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("dependency cycle: %s", strings.Join(append(path, key), " -> "))
		}
		state[key] = visiting
		path = append(path, key)
		for _, dep := range internalDeps(deps, key) {
			if err := visit(dep, path); err != nil {
				return err
			}
		}
		state[key] = done
		order = append(order, key)
		return nil
	}
	for _, key := range slices.Sorted(maps.Keys(deps)) {
		if err := visit(key, nil); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// internalDeps returns the keys of deps that key's references resolve to,
// sorted and without repeats, leaving out key itself.
func internalDeps(deps map[string][]string, key string) []string {
	byName := map[string]string{}
	for k := range deps {
		byName[imageName(k)] = k
	}
	var internal []string
	for _, ref := range deps[key] {
		if dep, ok := byName[imageName(ref)]; ok && dep != key && !slices.Contains(internal, dep) {
			internal = append(internal, dep)
		}
	}
	slices.Sort(internal)
	return internal
}

// imageName reduces a reference to its fully qualified name without tag or
// digest, "docker.io/lfedge/eve-alpine" for "lfedge/eve-alpine:<hash>".
func imageName(ref string) string {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return ref
	}
	return reference.TrimNamed(named).String()
}

// dumpBuildMulti writes the images of deps in build order, one per line,
// each followed by the packages it depends on.
func dumpBuildMulti(w io.Writer, deps map[string][]string) error {
	order, err := buildOrder(deps)
	if err != nil {
		return err
	}
	for _, image := range order {
		if internal := internalDeps(deps, image); len(internal) > 0 {
			fmt.Fprintf(w, "%s <- %s\n", image, strings.Join(internal, " "))
		} else {
			fmt.Fprintln(w, image)
		}
	}
	return nil
}
