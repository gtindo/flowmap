VERSION ?= 0.7.0
VERSION_NUMBER := $(patsubst v%,%,$(VERSION))
RELEASE_VERSION := v$(VERSION_NUMBER)
BINARY := flowmap
PACKAGE := ./cmd/flowmap
DIST := dist/$(RELEASE_VERSION)
LDFLAGS := -s -w -X main.version=$(RELEASE_VERSION)

.PHONY: build fmt lint test release linux-amd64 darwin-arm64 darwin-amd64 verify-release-toolchain checksums

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PACKAGE)

fmt:
	gofmt -w $$(find cmd internal -name '*.go' -type f)

lint:
	@test -z "$$(gofmt -l cmd internal)" || (echo "run 'make fmt' to format Go files" >&2; gofmt -l cmd internal >&2; exit 1)
	go vet ./...

test:
	go test ./...

release: test linux-amd64 darwin-arm64 darwin-amd64 verify-release-toolchain checksums
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

verify-release-toolchain:
	@for binary in \
		$(DIST)/flowmap_$(VERSION_NUMBER)_linux_amd64/flowmap \
		$(DIST)/flowmap_$(VERSION_NUMBER)_darwin_arm64/flowmap \
		$(DIST)/flowmap_$(VERSION_NUMBER)_darwin_amd64/flowmap; do \
		compiler="$$(go version -m "$$binary" 2>/dev/null | awk 'NR == 1 { print $$2 }')"; \
		case "$$compiler" in \
			go1.26|go1.26.*|go1.26-*) ;; \
			*) echo "release: $$binary was not built with Go 1.26 ($$compiler)" >&2; exit 1 ;; \
		esac; \
	done

checksums:
	@cd $(DIST) && if command -v sha256sum >/dev/null 2>&1; then sha256sum *.tar.gz > SHA256SUMS; else shasum -a 256 *.tar.gz > SHA256SUMS; fi
