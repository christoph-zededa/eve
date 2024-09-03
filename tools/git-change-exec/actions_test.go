// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestId(t *testing.T) {
	p := pillarTestAction{}

	id := id(p)
	if id != "pillarTestAction" {
		t.Fatalf("wrong id: %s\n", id)
	}
}
