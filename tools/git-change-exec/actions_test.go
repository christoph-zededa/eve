// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestId(t *testing.T) {
	a := newActionScript()

	_, err := a.interpreter.EvalPath("tests/gitChangeExecTest.go")
	if err != nil {
		panic(err)
	}
	a.setMethods()
	a.id = "foo"

	idString := id(a)
	if idString != "foo" {
		t.Fatalf("wrong id, got %s", idString)
	}
}
