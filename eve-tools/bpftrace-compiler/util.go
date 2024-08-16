// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"log"
	"os"

	"gopkg.in/yaml.v2"
)

type lkConf struct {
	kernel   string
	onboot   map[string]string // name -> image
	services map[string]string // name -> image
}

type lkConfYaml struct {
	Kernel struct {
		Image string `yaml:"image"`
	} `yaml:"kernel"`
	Onboot []struct {
		Name  string `yaml:"name"`
		Image string `yaml:"image"`
	} `yaml:"onboot"`
	Services []struct {
		Name  string `yaml:"name"`
		Image string `yaml:"image"`
	} `yaml:"services"`
}

func linuxkitYml2KernelConf(ymlBytes []byte) lkConf {
	var y lkConfYaml
	err := yaml.Unmarshal(ymlBytes, &y)
	if err != nil {
		log.Fatal(err)
	}

	l := lkConf{
		kernel:   "",
		onboot:   map[string]string{},
		services: map[string]string{},
	}
	l.kernel = y.Kernel.Image

	l.onboot = make(map[string]string)
	for _, container := range y.Onboot {
		l.onboot[container.Name] = container.Image
	}
	for _, container := range y.Services {
		l.services[container.Name] = container.Image
	}

	return l
}

func cleanArch(arch string) string {
	if arch == "x86_64" {
		arch = "amd64"
	}
	if arch == "aarch64" {
		arch = "arm64"
	}

	return arch
}

func copyFile(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)

	return err
}
