PROJECT_DIR=/go/src/github.com/seemethere/release-bot/utilities/createProject
DOCKER_RUN=docker run --rm -v "$(CURDIR)":"$(PROJECT_DIR)" -w "$(PROJECT_DIR)"

all: build

.PHONY: shell
shell:
	$(DOCKER_RUN) -it golang:1.8.3 bash

.PHONY: clean
clean:
	$(RM) -r 

build:
	go build 

.PHONY: build-image
build-image:
	docker build -t seemethere/createProject .
