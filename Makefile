# cairn Makefile

BINARY := cairn
VERSION := $(shell grep 'const version' main.go | sed 's/.*"\(.*\)".*/\1/')

# Default target: build for current OS/arch
.PHONY: build
build:
	go build -o $(BINARY) .

# Build for x86 Linux (linux/amd64)
.PHONY: build-linux
build-linux:
	GOOS=linux GOARCH=amd64 go build -o $(BINARY)-linux-amd64 .

# Build for 32-bit x86 Linux (optional)
.PHONY: build-linux-386
build-linux-386:
	GOOS=linux GOARCH=386 go build -o $(BINARY)-linux-386 .

# Clean build artifacts
.PHONY: clean
clean:
	rm -f $(BINARY) $(BINARY).exe $(BINARY)-linux-amd64 $(BINARY)-linux-386
