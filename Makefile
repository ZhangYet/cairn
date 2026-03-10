# cairn Makefile

BINARY := cairn
VERSION := $(shell grep 'const version' main.go | sed 's/.*"\(.*\)".*/\1/')
REQUIRED_GO := 1.23
GVM_SCRIPT := $(HOME)/.gvm/scripts/gvm

# Ensure Go 1.23 in this shell, then run the build. Each build target uses ONE shell so gvm use persists.
define ensure_go_and_build
	v=$$(go version 2>/dev/null | sed -n 's/.*go\([0-9]*\.[0-9]*\).*/\1/p'); \
	if [ "$$v" != "$(REQUIRED_GO)" ]; then \
		echo "Go version is $$v; need go$(REQUIRED_GO). Trying: gvm use go$(REQUIRED_GO)..."; \
		if [ -f "$(GVM_SCRIPT)" ]; then \
			. "$(GVM_SCRIPT)" && gvm use go$(REQUIRED_GO) || { echo "ERROR: Need Go $(REQUIRED_GO). Run: gvm install go$(REQUIRED_GO) && gvm use go$(REQUIRED_GO)"; exit 1; }; \
		else \
			echo "ERROR: Need Go $(REQUIRED_GO). gvm not found (no $(GVM_SCRIPT)). Install Go $(REQUIRED_GO) or install gvm and run: gvm install go$(REQUIRED_GO) && gvm use go$(REQUIRED_GO)"; exit 1; \
		fi; \
	fi; \
	$(1)
endef

# Default target: build for current OS/arch (check + build in same shell)
.PHONY: build
build:
	@$(call ensure_go_and_build,go build -o $(BINARY) .)

# Build for x86 Linux (linux/amd64)
.PHONY: build-linux
build-linux:
	@$(call ensure_go_and_build,GOOS=linux GOARCH=amd64 go build -o $(BINARY)-linux-amd64 .)

# Build for 32-bit x86 Linux (optional)
.PHONY: build-linux-386
build-linux-386:
	@$(call ensure_go_and_build,GOOS=linux GOARCH=386 go build -o $(BINARY)-linux-386 .)

# Check Go version only (no build) — single shell so gvm use persists for the echo
.PHONY: check-go
check-go:
	@v=$$(go version 2>/dev/null | sed -n 's/.*go\([0-9]*\.[0-9]*\).*/\1/p'); \
	if [ "$$v" != "$(REQUIRED_GO)" ]; then \
		echo "Go version is $$v; need go$(REQUIRED_GO). Trying: gvm use go$(REQUIRED_GO)..."; \
		if [ -f "$(GVM_SCRIPT)" ]; then \
			. "$(GVM_SCRIPT)" && gvm use go$(REQUIRED_GO) || { echo "ERROR: Need Go $(REQUIRED_GO). Run: gvm install go$(REQUIRED_GO) && gvm use go$(REQUIRED_GO)"; exit 1; }; \
		else \
			echo "ERROR: Need Go $(REQUIRED_GO). gvm not found."; exit 1; \
		fi; \
	fi; \
	echo "Go version OK: $$(go version)"

# Clean build artifacts
.PHONY: clean
clean:
	rm -f $(BINARY) $(BINARY).exe $(BINARY)-linux-amd64 $(BINARY)-linux-386
