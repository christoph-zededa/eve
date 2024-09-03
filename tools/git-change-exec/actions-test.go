// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"strings"
)

type pillarTestAction struct{}

func commonIgnore(path string) bool {
	if filepath.Ext(path) == "md" {
		return true
	}

	if filepath.Base(path) == "README" {
		return true
	}

	return false
}

func (b pillarTestAction) match(path string) bool {
	if commonIgnore(path) {
		return false
	}

	ignorePaths := []string{
		"pkg/pillar/agentlog/cmd/",
		"pkg/pillar/docs",
	}

	for _, ignorePath := range ignorePaths {
		if strings.HasPrefix(path, ignorePath) {
			return false
		}
	}
	return strings.HasPrefix(path, "pkg/pillar")
}

func (b pillarTestAction) do() error {
	return execMakeTest("pkg/pillar")
}

type getDepsTestAction struct{}

func (g getDepsTestAction) match(path string) bool {
	if commonIgnore(path) {
		return false
	}

	return strings.HasPrefix(path, "tools/get-deps")

}
func (g getDepsTestAction) do() error {
	return execMakeTest("tools/get-deps")
}

type gitChangeExecTest struct{}

func (g gitChangeExecTest) match(path string) bool {
	if commonIgnore(path) {
		return false
	}

	return strings.HasPrefix(path, "tools/git-change-exec")

}
func (g gitChangeExecTest) do() error {
	return execMakeTest("tools/git-change-exec")
}

type bpftraceCompilerExecTest struct{}

func (bpftraceCompilerExecTest) match(path string) bool {
	if commonIgnore(path) {
		return false
	}

	return strings.HasPrefix(path, "eve-tools/bpftrace-compiler")

}
func (bpftraceCompilerExecTest) do() error {
	return execMakeTest("eve-tools/bpftrace-compiler")
}
