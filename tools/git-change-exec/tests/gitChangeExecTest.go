// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var paths []string

func init() {
	fmt.Println(">>> initializing gitChangeExecTest")

	paths = make([]string, 0)
}

func execCmdWithDefaults(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return cmd
}

func match(path string) bool {
	paths = append(paths, path)
	return strings.HasPrefix(path, "tools/git-change-exec")

}
func do() error {
	fmt.Printf("paths were: %=v\n", paths)
	return execCmdWithDefaults("go", "test", "-C", "tools/git-change-exec", "-v").Run()
}
