// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"github.com/go-git/go-git/v5/config"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type lintSpdx struct {
	extsMap      map[string]func(path string)
	pathsToFix   []string
	ownPath      string
	organization string
}

func (s *lintSpdx) dontfix(path string) {
	log.Printf("Cannot fix %s ...\n", path)
}

func (s *lintSpdx) copyright(commentIndicator string) []string {
	copyrightLines := []string{
		fmt.Sprintf("%s Copyright (c) %d %s, Inc.\n", commentIndicator, time.Now().Year(), s.organization),
		fmt.Sprintf("%s SPDX-License-Identifier: Apache-2.0\n\n", commentIndicator),
	}

	return copyrightLines
}

func (s *lintSpdx) yamlfix(path string) {
	log.Printf("Fixing %s ...\n", path)
	prepend(path, s.copyright("#"))
}

func (s *lintSpdx) gofix(path string) {
	log.Printf("Fixing %s ...\n", path)
	prepend(path, s.copyright("//"))
}

func (s *lintSpdx) dockerfilefix(path string) {
	log.Printf("Fixing %s ...\n", path)
	prepend(path, s.copyright("#"))
}

func prepend(path string, license []string) {
	backupFh, err := os.CreateTemp("/var/tmp", "git-change-exec-spdx-fix")
	if err != nil {
		log.Fatalf("could not create temp file: %v", err)
	}
	backupPath := backupFh.Name()
	defer os.Remove(backupPath)

	for _, line := range license {
		fmt.Fprint(backupFh, line)
	}

	origFh, err := os.Open(path)
	if err != nil {
		log.Fatalf("could not open original file %s: %v", path, err)
	}

	_, err = io.Copy(backupFh, origFh)
	if err != nil {
		log.Fatalf("could not copy: %v", err)
	}

	backupFh.Close()
	origFh.Close()

	err = copyFile(backupPath, path)
	if err != nil {
		log.Fatalf("could not rename %s -> %s: %v", backupPath, path, err)
	}

}

func readGitConfigOrganization() string {
	cfg, err := config.LoadConfig(config.GlobalScope)
	if err != nil {
		panic(err)
	}

	for _, sec := range cfg.Raw.Sections {
		if sec.Name != "user" {
			continue
		}
		// codespell:ignore
		organizations := sec.OptionAll("organization")

		for _, organization := range organizations {
			if organization != "" {
				return organization
			}
		}
	}

	return ""
}

func (s *lintSpdx) init() {
	var err error

	s.extsMap = map[string]func(path string){
		".sh":        s.dontfix,
		".go":        s.gofix,
		".c":         s.dontfix,
		".h":         s.dontfix,
		".py":        s.dontfix,
		".rs":        s.dontfix,
		".yaml":      s.yamlfix,
		".yml":       s.yamlfix,
		"Dockerfile": s.dockerfilefix,
	}

	s.pathsToFix = make([]string, 0)

	s.ownPath, err = os.Executable()
	if err != nil {
		log.Fatalf("could not determine executable path: %v", err)
	}

	s.organization = readGitConfigOrganization()

}

func (s *lintSpdx) pathMatch(path string) (func(path string), bool) {
	f, found := s.extsMap[filepath.Ext(path)]
	if !found {
		f, found = s.extsMap[filepath.Base(path)]
	}

	return f, found
}

func (s *lintSpdx) hasSpdx(path string) bool {
	scriptPath := filepath.Join(filepath.Dir(s.ownPath), "..", "spdx-check.sh")

	cmd := exec.Command(scriptPath, path)
	err := cmd.Run()

	return err == nil
}

func (s *lintSpdx) match(path string) bool {
	if s.extsMap == nil {
		s.init()
	}

	if strings.Contains(path, "/vendor/") {
		return false
	}
	_, found := s.pathMatch(path)
	if !found {
		return false
	}
	if !s.hasSpdx(path) {
		s.pathsToFix = append(s.pathsToFix, path)
		return true
	}

	return false
}

func (s *lintSpdx) do() error {
	if s.organization == "" {
		log.Printf("could not read organization from git config, cannot fix copyrights")
		return nil
	}

	for _, path := range s.pathsToFix {
		extFixFunc, found := s.pathMatch(path)
		if !found {
			continue
		}

		extFixFunc(path)
	}

	return nil
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
