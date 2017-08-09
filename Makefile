.PHONY: check
check:
	docker run \
		-v $(CURDIR):/go/src/github.com/seemethere/release-bot \
		-w /go/src/github.com/seemethere/release-bot \
		dnephin/gometalinter \
		--vendor --tests --disable-all \
		-E gofmt -E vet -E goimports -E golint ./...

.PHONY: build-image
build-image:
	docker build -t seemethere/release-bot .

.PHONY: clean
clean:
	$(RM) -r build

build:
	mkdir -p build
	go build -o build/release-bot main.go

.PHONY: run-dev
run-dev: clean check build
	./build/release-bot -debug
