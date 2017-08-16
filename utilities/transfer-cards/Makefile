PROJECT_DIR=/go/src/github.com/seemethere/release-bot/utilities/transfer-cards
DOCKER_RUN=docker run --rm -v "$(CURDIR)":"$(PROJECT_DIR)" -w "$(PROJECT_DIR)"

.PHONY: shell
shell:
	$(DOCKER_RUN) -it golang:1.8.3 bash

.PHONY: clean
clean:
	$(RM) -r build

build:
	go build -o build/transfer-cards main.go
