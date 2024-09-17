// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strings"
)

type pillarTestAction struct{}

func (b pillarTestAction) matchDiff(path string, ld lineDiff) bool {
	prefix := ""
	if ld.op == lineAdd {
		prefix = "+++"
	}
	if ld.op == lineDel {
		prefix = "---"
	}
	fmt.Printf("%s %s:%d: type:%s %s %s \n", prefix, path, ld.lineNumber, ld.typeOfLine.String(), ld.op, ld.line)

	return false
}

func (b pillarTestAction) matchPath(path string) bool {
	return strings.HasPrefix(path, "pkg/pillar")
}

func (b pillarTestAction) do() error {
	return execCmdWithDefaults("make", "-C", "pkg/pillar", "test").Run()
}

type getDepsTestAction struct{}

func (g getDepsTestAction) matchPath(path string) bool {
	return strings.HasPrefix(path, "tools/get-deps")

}
func (g getDepsTestAction) do() error {
	return execCmdWithDefaults("go", "test", "-C", "tools/get-deps", "-v").Run()
}

type gitChangeExecTest struct{}

func (g gitChangeExecTest) matchPath(path string) bool {
	return strings.HasPrefix(path, "tools/git-change-exec")

}
func (g gitChangeExecTest) do() error {
	return execCmdWithDefaults("go", "test", "-C", "tools/git-change-exec", "-v").Run()
}

type bpftraceCompilerExecTest struct{}

func (bpftraceCompilerExecTest) matchPath(path string) bool {
	return strings.HasPrefix(path, "eve-tools/bpftrace-compiler")

}
func (bpftraceCompilerExecTest) do() error {
	return execCmdWithDefaults("make", "-C", "eve-tools/bpftrace-compiler", "test").Run()
}
