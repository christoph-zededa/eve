# Copyright (c) 2024 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0

cmd/git-exec/git-exec: pkg/*.go cmd/git-exec/*.go
	go build -C cmd/git-exec/

.PHONY: clean test

clean:
	rm -fv ./cmd/git-exec/git-exec

test:
	go test -C pkg/ -v
	go test -C pkg -fuzz=^FuzzLCSDiff$$ -v -fuzztime=1m
	go test -C pkg -fuzz=^FuzzLCS$$ -v -fuzztime=1m
