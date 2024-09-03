// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"reflect"
)

type action interface {
	match(path string) bool
	do() error
}

func id(i any) string {
	ty := reflect.TypeOf(i)
	if ty.Name() == "" {
		ty = reflect.TypeOf(i).Elem()
	}
	return ty.Name()
}

func execCmdWithDefaults(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return cmd
}

func execMakeTest(path string) error {
	return execCmdWithDefaults("make", "-C", path, "test").Run()
}

// Do not forget to add your Action HERE
var actions = map[string][]action{
	"test": {
		pillarTestAction{},
		gitChangeExecTest{},
		getDepsTestAction{},
		bpftraceCompilerExecTest{},
	},
	"lint": {
		&lintSpdx{},
	},
}
