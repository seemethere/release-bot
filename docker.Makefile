MOUNT_PATH="/go/src/github.com/docker/release-bot"
DOCKER_RUN_FLAGS?=""
DOCKER_RUN=docker run --rm -it -v "$(CURDIR)":"$(MOUNT_PATH)" -w "$(MOUNT_PATH)" $(DOCKER_RUN_FLAGS) golang:1.8.3

clean:
	$(DOCKER_RUN) make clean

build:
	$(DOCKER_RUN) make build

run-dev:
	$(DOCKER_RUN) make run-dev
