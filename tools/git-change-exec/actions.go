// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
)

type action interface {
	match(path string) bool
	do() error
}

func id(i any) string {
	ty := reflect.TypeOf(i)
	return ty.Name()
}

func execCmdWithDefaults(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return cmd
}

// Do not forget to add your Action HERE
var actions = []action{
	pillarTestAction{},
	gitChangeExecTest{},
	getDepsTestAction{},
}

type pillarTestAction struct{}

func (b pillarTestAction) match(path string) bool {
	return strings.HasPrefix(path, "pkg/pillar")
}

func (b pillarTestAction) do() error {
	return execCmdWithDefaults("make", "-C", "pkg/pillar", "test").Run()
}

type getDepsTestAction struct{}

func (g getDepsTestAction) match(path string) bool {
	return strings.HasPrefix(path, "tools/get-deps")

}
func (g getDepsTestAction) do() error {
	return execCmdWithDefaults("go", "test", "-C", "tools/get-deps", "-v").Run()
}

type gitChangeExecTest struct{}

func (g gitChangeExecTest) match(path string) bool {
	return strings.HasPrefix(path, "tools/git-change-exec")

}
func (g gitChangeExecTest) do() error {
	return execCmdWithDefaults("go", "test", "-C", "tools/git-change-exec", "-v").Run()
}
