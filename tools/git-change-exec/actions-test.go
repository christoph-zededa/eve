// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import "strings"

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

type bpftraceCompilerExecTest struct{}

func (bpftraceCompilerExecTest) match(path string) bool {
	return strings.HasPrefix(path, "eve-tools/bpftrace-compiler")

}
func (bpftraceCompilerExecTest) do() error {
	return execCmdWithDefaults("make", "-C", "eve-tools/bpftrace-compiler", "test").Run()
}
