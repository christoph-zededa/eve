// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0
package zboot

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"

	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/sirupsen/logrus"
)

func findExePath(exe string) string {
	pathEnv := os.Getenv("PATH")

	paths := strings.Split(pathEnv, ":")

	for _, path := range paths {
		fullPath := filepath.Join(path, exe)
		_, err := os.Stat(fullPath)
		if err == nil {
			return fullPath
		}
	}

	return ""
}

// let's do our own pkill, as handling different versions of it is a pain
// f.e. procps pkill needs '-f' if the string is longer than 15 characters
// but busybox pkill doesn't work with '-f'
func pkill(path string) bool {
	dirEntries, err := os.ReadDir("/proc")
	if err != nil {
		panic(err)
	}

	for _, dirEntry := range dirEntries {
		var pid int
		pidString := filepath.Base(dirEntry.Name())
		if pid, err = strconv.Atoi(pidString); err != nil {
			continue
		}
		cmdlinePath := filepath.Join("/proc", dirEntry.Name(), "cmdline")
		cmdline, err := os.ReadFile(cmdlinePath)
		if err != nil {
			continue
		}
		exe := string(bytes.Split(cmdline, []byte{0})[0])
		if len(exe) == 0 {
			continue
		}
		if path != exe {
			continue
		}

		syscall.Kill(pid, syscall.SIGUSR2)
		return true
	}

	return false
}

func TestExecWithRetry(t *testing.T) {
	t.Parallel()

	logger := logrus.New()
	logBuf := &bytes.Buffer{}
	logger.Out = logBuf
	log := base.NewSourceLogObject(logger, "zboot_test", -255)
	data, err := os.ReadFile(findExePath("sleep"))
	if err != nil {
		panic(err)
	}
	tmpDir, err := os.MkdirTemp("/tmp", "sleep-TestExecWithRetry")
	defer os.RemoveAll(tmpDir)
	tmpFile := filepath.Join(tmpDir, "sleep")
	err = os.WriteFile(tmpFile, data, 0700)
	if err != nil {
		panic(err)
	}

	var pkillOutput []byte
	go func() {
		for !pkill(tmpFile) {
			time.Sleep(1 * time.Second)
		}
		// remove the binary, otherwise execWithRetry would retry endlessly
		os.Remove(tmpFile)
	}()

	_, err = execWithRetry(log, tmpFile, "60")
	if err != nil {
		t.Log(err)
	}

	logOutput := logBuf.String()

	if !strings.Contains(logOutput, "because of signal user defined signal 2") {
		t.Fatalf("Killed sleep with USR2 went unnoticed, pkill output is: %s", string(pkillOutput))
	}
	t.Log(logBuf.String())
}

func TestHandlingSignals(t *testing.T) {
	s := signalChecker{
		signalNames:        []string{},
		checkedSignalNames: []string{},
	}

	paths := make([]string, 0)

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("could not call runtime.Caller")
	}

	pillarPath := filepath.Join(filepath.Dir(filename), "..")
	if filepath.Base(pillarPath) != "pillar" {
		t.Fatal("unexpected directory structure")
	}

	filepath.WalkDir(pillarPath, func(path string, d fs.DirEntry, err error) error {
		if filepath.Base(path) == "vendor" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".go" {
			paths = append(paths, path)
		}
		return nil
	})
	s.run(paths)

	checkedSignalNamesMap := make(map[string]struct{})
	for _, checkedSignalName := range s.checkedSignalNames {
		checkedSignalNamesMap[checkedSignalName] = struct{}{}
	}

	for _, signalName := range s.signalNames {
		_, found := checkedSignalNamesMap[signalName]
		if !found {
			t.Fatalf("%s is not checked\n", signalName)
		}
	}
}

type signalChecker struct {
	signalNames        []string
	checkedSignalNames []string
}

func (s *signalChecker) run(paths []string) {
	for _, srcPath := range paths {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, srcPath, nil, 0)
		// f is of type *ast.File
		if err != nil {
			panic(err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				s.collectSignalNames(x)
			case *ast.FuncDecl:
				s.checkExecWithRetry(x)
			}
			return true
		})

	}
}

func (s *signalChecker) checkExecWithRetry(x *ast.FuncDecl) {
	if x.Name.Name != "execWithRetry" {
		return
	}
	isRetrySignalsFunc := false
	for _, le := range x.Body.List {
		assign, ok := le.(*ast.AssignStmt)
		if !ok {
			continue
		}
		for _, lh := range assign.Lhs {
			ident, ok := lh.(*ast.Ident)
			if !ok {
				continue
			}
			if ident.Name == "retrySignals" {
				isRetrySignalsFunc = true
				break
			}
		}
		if !isRetrySignalsFunc {
			return
		}
		for _, rh := range assign.Rhs {
			comp, ok := rh.(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, elt := range comp.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				se, ok := kv.Key.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				s.checkedSignalNames = append(s.checkedSignalNames, fmt.Sprintf("%s.%s", se.X, se.Sel))
			}
		}
	}
}

func (s *signalChecker) collectSignalNames(x *ast.CallExpr) {
	selexpr, ok := x.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	ident, ok := selexpr.X.(*ast.Ident)
	if !ok || ident.Name != "signal" {
		return
	}
	if selexpr.Sel.Name == "Notify" || selexpr.Sel.Name == "NotifyContext" {

		for _, arg := range x.Args[1:] {
			se, ok := arg.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			s.signalNames = append(s.signalNames, fmt.Sprintf("%s.%s", se.X, se.Sel))
		}
	}
}
