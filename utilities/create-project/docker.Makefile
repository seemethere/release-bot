MOUNT_PATH=/go/src/github.com/seemethere/release-bot/utilities/create-project
DOCKER_RUN_FLAGS?=
DOCKER_IMAGE=golang:1.8.3
DOCKER_RUN=docker run --rm -it -v "$(CURDIR)":"$(MOUNT_PATH)" -w "$(MOUNT_PATH)" $(DOCKER_RUN_FLAGS)

.PHONY: clean
clean:
	$(DOCKER_RUN) $(DOCKER_IMAGE) make clean

build:
	$(DOCKER_RUN) $(DOCKER_IMAGE) make build
