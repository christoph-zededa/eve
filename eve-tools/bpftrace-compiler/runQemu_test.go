package main

import "testing"

func TestRunDebug(t *testing.T) {
	t.Parallel()
	q := newQemuAmd64Runner("imageDir", "bpfPath", "aotPath")
	q.units = append(q.units, "compile", "shell")

	args := q.runDebugArgs("/share/dir")

	t.Logf("args=%+q", args)

	// TODO: add real test
}
