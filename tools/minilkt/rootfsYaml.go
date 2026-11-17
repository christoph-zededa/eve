package main

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v2"
)

type RootFSKernel struct {
	Image string `yaml:"image"`
}
type RootFSImage struct {
	Name  string `yaml:"name"`
	Image string `yaml:"image"`
}
type RootFSYAML struct {
	Kernel   RootFSKernel
	Init     []string      `yaml:"init"`
	Onboot   []RootFSImage `yaml:"onboot"`
	Services []RootFSImage `yaml:"services"`
	Volumes  []RootFSImage `yaml:"volumes"`
}

func parseRootFSYaml(path string) RootFSYAML {
	bs, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}

	r := RootFSYAML{}

	yaml.Unmarshal(bs, &r)

	return r
}

// without tag
func (ry RootFSYAML) images() []string {
	imgs := make([]string, 0)

	if ry.Kernel.Image != "" {
		imgs = append(imgs, ry.Kernel.Image)
	}
	imgs = append(imgs, ry.Init...)

	for _, ris := range [][]RootFSImage{ry.Onboot, ry.Services, ry.Volumes} {
		for _, ri := range ris {
			imgs = append(imgs, ri.Image)
		}
	}

	for i := range imgs {
		org, name, _ := splitImage(imgs[i])
		imgs[i] = fmt.Sprintf("%s/%s", org, name)
	}
	return imgs
}
