# Process Pillz Makefile

# Project information
BINARY_NAME = process_pillz
VERSION := $(shell cat VERSION 2>/dev/null || echo "dev")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')

# Build flags
LDFLAGS = -ldflags "-X main.Version=$(VERSION) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildTime=$(BUILD_TIME)"
GOFLAGS = -trimpath

# Installation paths
PREFIX ?= /usr
BINDIR = $(PREFIX)/bin
SYSTEMD_USER_DIR = /etc/systemd/user
SHARE_DIR = $(PREFIX)/share/$(BINARY_NAME)
DOC_DIR = $(PREFIX)/share/doc/$(BINARY_NAME)

# Installation variables for packaging
DESTDIR ?=

.PHONY: all build clean install uninstall dev test version help

# Default target
all: build

# Build the binary
build:
	@echo "Building $(BINARY_NAME) $(VERSION)..."
	go build $(GOFLAGS) $(LDFLAGS) -o $(BINARY_NAME) .

# Development build (no optimization, with debug info)
dev:
	@echo "Building $(BINARY_NAME) for development..."
	go build -gcflags="all=-N -l" $(LDFLAGS) -o $(BINARY_NAME) .

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -f $(BINARY_NAME)
	go clean

# Install system-wide (requires root)
install: build
	@echo "Installing $(BINARY_NAME)..."
	install -d $(DESTDIR)$(BINDIR)
	install -m 755 $(BINARY_NAME) $(DESTDIR)$(BINDIR)/
	
	@echo "Installing systemd user services..."
	install -d $(DESTDIR)$(SYSTEMD_USER_DIR)
	install -m 644 systemd/user/$(BINARY_NAME).service $(DESTDIR)$(SYSTEMD_USER_DIR)/
	install -m 644 systemd/user/$(BINARY_NAME)-restarter.service $(DESTDIR)$(SYSTEMD_USER_DIR)/
	install -m 644 systemd/user/$(BINARY_NAME)-restarter.path $(DESTDIR)$(SYSTEMD_USER_DIR)/
	
	@echo "Installing example configuration..."
	install -d $(DESTDIR)$(SHARE_DIR)
	install -m 644 $(BINARY_NAME).yaml $(DESTDIR)$(SHARE_DIR)/$(BINARY_NAME).yaml.example
	
	@echo "Creating documentation directory..."
	install -d $(DESTDIR)$(DOC_DIR)
	
	@echo ""
	@echo "Installation complete!"
	@echo ""
	@echo "To enable the service for your user:"
	@echo "  systemctl --user daemon-reload"
	@echo "  systemctl --user enable $(BINARY_NAME)"
	@echo "  systemctl --user start $(BINARY_NAME)"
	@echo ""
	@echo "Example configuration installed to: $(SHARE_DIR)/$(BINARY_NAME).yaml.example"
	@echo "Copy it to ~/.config/$(BINARY_NAME).yaml and customize as needed."

# Uninstall
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY_NAME)
	rm -f $(DESTDIR)$(SYSTEMD_USER_DIR)/$(BINARY_NAME).service
	rm -f $(DESTDIR)$(SYSTEMD_USER_DIR)/$(BINARY_NAME)-restarter.service
	rm -f $(DESTDIR)$(SYSTEMD_USER_DIR)/$(BINARY_NAME)-restarter.path
	rm -rf $(DESTDIR)$(SHARE_DIR)
	rm -rf $(DESTDIR)$(DOC_DIR)
	@echo "Uninstallation complete!"

# Install to local bin for development
dev-install: dev
	@echo "Installing $(BINARY_NAME) to local bin..."
	mkdir -p ~/bin
	cp $(BINARY_NAME) ~/bin/
	@echo "$(BINARY_NAME) installed to ~/bin/"

# Run tests (placeholder for future tests)
test:
	@echo "Running tests..."
	go test ./...

# Show version information
version:
	@echo "$(BINARY_NAME) version $(VERSION)"
	@echo "Git commit: $(GIT_COMMIT)"
	@echo "Build time: $(BUILD_TIME)"

# Show help
help:
	@echo "Process Pillz Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  build      - Build the binary"
	@echo "  dev        - Build with debug info for development"
	@echo "  clean      - Clean build artifacts"
	@echo "  install    - Install system-wide (requires sudo)"
	@echo "  uninstall  - Remove installed files"
	@echo "  dev-install- Install to ~/bin for development"
	@echo "  test       - Run tests"
	@echo "  version    - Show version information"
	@echo "  help       - Show this help"
	@echo ""
	@echo "Installation paths:"
	@echo "  Binary: $(BINDIR)/$(BINARY_NAME)"
	@echo "  Systemd: $(SYSTEMD_USER_DIR)/"
	@echo "  Config example: $(SHARE_DIR)/"
	@echo ""
	@echo "Environment variables:"
	@echo "  PREFIX     - Installation prefix (default: /usr)"
	@echo "  DESTDIR    - Destination directory for packaging"
