BINARY = wvl
MODULE = ./cmd/waveloom
LOCALBIN ?= $(HOME)/go/bin

.PHONY: build
build:
	go build -o ./bin/$(BINARY) $(MODULE)

.PHONY: install
install:
	go build -o $(LOCALBIN)/$(BINARY) $(MODULE)

.PHONY: run
run:
	go run $(MODULE)

.PHONY: test
test:
	go test ./...

.PHONY: test-integration
test-integration:
	go test -tags=integration ./... -timeout 300s

.PHONY: clean
clean:
	rm -rf ./bin/
