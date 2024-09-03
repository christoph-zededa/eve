# Copyright (c) 2024 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0

SOURCES=actions.go actions-lint.go actions-test.go actions_test.go main.go
git-change-exec: $(SOURCES)
	go build

.PHONY: clean

clean:
	rm -fv git-change-exec

test:$(SOURCES)
	go test
