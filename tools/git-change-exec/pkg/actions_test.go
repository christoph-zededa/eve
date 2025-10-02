// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package pkg

import "testing"

func TestId(t *testing.T) {
	p := LintSpdx{}

	id := Id(p)
	if id != "LintSpdx" {
		t.Fatalf("wrong id: %s\n", id)
	}
}
