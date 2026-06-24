BINARY = wvl
MODULE = ./cmd/waveloom
LOCALBIN ?= $(HOME)/go/bin
VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
LDFLAGS = -s -w -X main.version=$(VERSION)

# Release matrix
GOOSES = linux darwin
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

.PHONY: test
test:
	go test ./...

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
			tar -czf $(DIST_DIR)/$(BINARY)_$${GOOS}_$${GOARCH}.tar.gz \
				-C $(DIST_DIR) $(BINARY); \
			rm $(DIST_DIR)/$(BINARY); \
		done; \
	done
	@cd $(DIST_DIR) && shasum -a 256 *.tar.gz > checksums.txt
	@echo "Done → $(DIST_DIR)/"

.PHONY: clean
clean:
	rm -rf ./bin/ $(DIST_DIR)
