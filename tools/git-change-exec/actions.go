// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"reflect"
)

type opActionLine uint8

const (
	opActionLineFullAdd = iota
	opActionLineFullDel
	opActionLineAdd
	opActionLineDel
)

type actionPath interface {
	matchPath(path string) bool
}
type actionDiff interface {
	matchDiff(path string, ld lineDiff) bool
}
type action interface {
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

// Do not forget to add your Action HERE
var actions = map[string][]action{
	"test": []action{
		pillarTestAction{},
		gitChangeExecTest{},
		getDepsTestAction{},
		bpftraceCompilerExecTest{},
	},
	"lint": []action{
		&lintSpdx{},
	},
}
