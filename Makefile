VERSION ?= 0.2.0
VERSION_NUMBER := $(patsubst v%,%,$(VERSION))
RELEASE_VERSION := v$(VERSION_NUMBER)
BINARY := flowmap
PACKAGE := ./cmd/flowmap
DIST := dist/$(RELEASE_VERSION)
LDFLAGS := -s -w -X main.version=$(RELEASE_VERSION)

.PHONY: build test release linux-amd64 darwin-arm64 darwin-amd64 checksums

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PACKAGE)

test:
	go test ./...

release: test linux-amd64 darwin-arm64 darwin-amd64 checksums
	@echo "Release $(RELEASE_VERSION) is ready in $(DIST)"

linux-amd64:
	@mkdir -p $(DIST)/flowmap_$(VERSION_NUMBER)_linux_amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/flowmap_$(VERSION_NUMBER)_linux_amd64/flowmap $(PACKAGE)
	@cp README.md USER_GUIDE.md $(DIST)/flowmap_$(VERSION_NUMBER)_linux_amd64/
	@tar -C $(DIST) -czf $(DIST)/flowmap_$(VERSION_NUMBER)_linux_amd64.tar.gz flowmap_$(VERSION_NUMBER)_linux_amd64

darwin-arm64:
	@mkdir -p $(DIST)/flowmap_$(VERSION_NUMBER)_darwin_arm64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/flowmap_$(VERSION_NUMBER)_darwin_arm64/flowmap $(PACKAGE)
	@cp README.md USER_GUIDE.md $(DIST)/flowmap_$(VERSION_NUMBER)_darwin_arm64/
	@tar -C $(DIST) -czf $(DIST)/flowmap_$(VERSION_NUMBER)_darwin_arm64.tar.gz flowmap_$(VERSION_NUMBER)_darwin_arm64

darwin-amd64:
	@mkdir -p $(DIST)/flowmap_$(VERSION_NUMBER)_darwin_amd64
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/flowmap_$(VERSION_NUMBER)_darwin_amd64/flowmap $(PACKAGE)
	@cp README.md USER_GUIDE.md $(DIST)/flowmap_$(VERSION_NUMBER)_darwin_amd64/
	@tar -C $(DIST) -czf $(DIST)/flowmap_$(VERSION_NUMBER)_darwin_amd64.tar.gz flowmap_$(VERSION_NUMBER)_darwin_amd64

checksums:
	@cd $(DIST) && if command -v sha256sum >/dev/null 2>&1; then sha256sum *.tar.gz > SHA256SUMS; else shasum -a 256 *.tar.gz > SHA256SUMS; fi
