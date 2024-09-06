// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
	"github.com/traefik/yaegi/stdlib/syscall"
	"github.com/traefik/yaegi/stdlib/unrestricted"
	"github.com/traefik/yaegi/stdlib/unsafe"
)

type action interface {
	match(path string) bool
	do() error
}

var actions []action

func id(i any) string {
	as, ok := i.(actionScript)
	if ok {
		return as.id
	}
	ty := reflect.TypeOf(i)
	name := ty.Name()
	if name == "" {
		panic("no name")
	}

	return name
}

type actionScript struct {
	interpreter *interp.Interpreter
	id          string
	runDo       func() error
	runMatch    func(string) bool
}

func (a *actionScript) setMethods() {
	doRefVal, err := a.interpreter.Eval("do")
	if err != nil {
		panic(err)
	}

	a.runDo = doRefVal.Interface().(func() error)
	matchRefVal, err := a.interpreter.Eval("match")
	if err != nil {
		panic(err)
	}
	a.runMatch = matchRefVal.Interface().(func(string) bool)
}

func (a actionScript) do() error {
	return a.runDo()
}

func (a actionScript) match(path string) bool {
	return a.runMatch(path)
}

func newActionScript() actionScript {
	var as actionScript

	as.interpreter = interp.New(interp.Options{Unrestricted: true})

	if err := as.interpreter.Use(stdlib.Symbols); err != nil {
		panic(err)
	}
	if err := as.interpreter.Use(syscall.Symbols); err != nil {
		panic(err)
	}
	if err := as.interpreter.Use(unsafe.Symbols); err != nil {
		panic(err)
	}
	// Use of unrestricted symbols should always follow stdlib and syscall symbols, to update them.
	if err := as.interpreter.Use(unrestricted.Symbols); err != nil {
		panic(err)
	}

	return as
}

func loadActions(dir string) {
	actions = make([]action, 0)

	entries, err := os.ReadDir(dir)
	if err != nil {
		panic(err)
	}

	for _, entry := range entries {
		a := newActionScript()
		if filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		_, err := a.interpreter.EvalPath(path)
		if err != nil {
			panic(err)
		}
		a.id = entry.Name()
		a.setMethods()
		fmt.Printf(">>> id %s\n", a.id)

		actions = append(actions, a)
	}
}
