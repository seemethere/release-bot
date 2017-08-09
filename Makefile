.PHONY: clean
clean:
	$(RM) -r build

build:
	mkdir -p build
	go build -o build/release-bot main.go

.PHONY: run-dev
run-dev: clean build
	./build/release-bot -debug
