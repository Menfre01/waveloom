BINARY = waveloom
MODULE = ./cmd/waveloom
LOCALBIN ?= $(HOME)/go/bin
VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
LDFLAGS = -s -w -X main.Version=$(VERSION) -X github.com/Menfre01/waveloom/pkg/session.BuildVersion=$(VERSION)

# Release matrix
GOOSES = linux darwin windows
GOARCHES = amd64 arm64
DIST_DIR = dist

.PHONY: build
build:
	go build -ldflags "$(LDFLAGS)" -o ./bin/$(BINARY) $(MODULE)

.PHONY: install
install:
	go build -ldflags "$(LDFLAGS)" -o $(LOCALBIN)/$(BINARY) $(MODULE)

.PHONY: run
run:
	go run $(MODULE)

.PHONY: lint
lint:
	golangci-lint run --timeout=5m

.PHONY: test
test:
	go test ./pkg/... ./cmd/...

.PHONY: test-integration
test-integration:
	go test -tags=integration ./... -timeout 300s

.PHONY: release
release:
	@rm -rf $(DIST_DIR)
	@mkdir -p $(DIST_DIR)
	@for GOOS in $(GOOSES); do \
		for GOARCH in $(GOARCHES); do \
			echo "→ Building $$GOOS/$$GOARCH ..."; \
			GOOS=$$GOOS GOARCH=$$GOARCH CGO_ENABLED=0 \
				go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY) $(MODULE); \
			if [ "$$GOOS" = "windows" ]; then \
				mv $(DIST_DIR)/$(BINARY) $(DIST_DIR)/$(BINARY).exe; \
				cd $(DIST_DIR) && zip $(BINARY)_$${GOOS}_$${GOARCH}.zip $(BINARY).exe && rm $(BINARY).exe; \
				cd $(CURDIR); \
			else \
				tar -czf $(DIST_DIR)/$(BINARY)_$${GOOS}_$${GOARCH}.tar.gz \
					-C $(DIST_DIR) $(BINARY); \
				rm $(DIST_DIR)/$(BINARY); \
			fi; \
		done; \
	done
	@cd $(DIST_DIR) && shasum -a 256 *.tar.gz *.zip > checksums.txt
	@echo "Done → $(DIST_DIR)/"

.PHONY: homebrew-formula
homebrew-formula:
	@chmod +x .github/scripts/generate-formula.sh
	.github/scripts/generate-formula.sh > $(DIST_DIR)/waveloom.rb
	@echo "Formula generated → $(DIST_DIR)/waveloom.rb"

.PHONY: clean
clean:
	rm -rf ./bin/ $(DIST_DIR)
