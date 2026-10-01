# Run inside `nix develop`. The vision helper builds with Xcode's Swift toolchain.

BIN := bin

.PHONY: build test vet clean

build: $(BIN)/sermon $(BIN)/sermon-vision

$(BIN)/sermon: $(shell find cmd internal -name '*.go' -o -name '*.md' -o -name '*.html') go.mod go.sum
	go build -o $@ ./cmd/sermon

$(BIN)/sermon-vision: $(shell find vision/Sources -name '*.swift') vision/Package.swift
	cd vision && swift build -c release --quiet
	mkdir -p $(BIN) && cp vision/.build/release/sermon-vision $@

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf $(BIN) vision/.build
