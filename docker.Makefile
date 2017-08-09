MOUNT_PATH=/go/src/github.com/docker/release-bot
DOCKER_RUN_FLAGS?=
DOCKER_IMAGE=golang:1.8.3
DOCKER_RUN=docker run --rm -it -v "$(CURDIR)":"$(MOUNT_PATH)" -w "$(MOUNT_PATH)" $(DOCKER_RUN_FLAGS)

.PHONY: clean
clean:
	$(DOCKER_RUN) $(DOCKER_IMAGE) make clean

build:
	$(DOCKER_RUN) $(DOCKER_IMAGE) make build

.PHONY: run-dev
run-dev:
	$(DOCKER_RUN) \
		-e RELEASE_BOT_WEBHOOK_SECRET \
		-e RELEASE_BOT_GITHUB_TOKEN \
		-e RELEASE_BOT_DEBUG="TRUE" \
		-p 8080:8080 \
		-d \
		$(DOCKER_IMAGE) \
		make run-dev
