all: *.go go.sum go.mod
	go build
docker:
	colima ssh -- sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 || true
	docker build --build-arg UID=$(shell id -u) --build-arg GID=$(shell id -g) --build-arg USER=$(shell id -nu) --build-arg HOME=${HOME} -t minilkt .
	docker run -v $(shell git rev-parse --show-toplevel):$(shell git rev-parse --show-toplevel) \
		-v minilkt-data:$(shell realpath ${HOME}/.minilkt) \
		-v $(shell realpath ${HOME}/.docker):$(shell realpath ${HOME}/.docker) \
		--workdir $(shell git rev-parse --show-toplevel) \
		--privileged -it minilkt

docker-clean:
	docker volume rm minilkt-data
