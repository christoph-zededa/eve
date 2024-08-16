bpftrace-compiler: *.go
	go build

.PHONY: test

test:
	go test -test.timeout 30m -v -parallel $(shell nproc) -race .
