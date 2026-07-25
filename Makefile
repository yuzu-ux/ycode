GO ?= go
VERSION ?= 0.1.0-dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test check install benchmark clean

build:
	@mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/ycode ./cmd/ycode

test:
	$(GO) test -race ./...

check:
	@test -z "$$(gofmt -l .)"
	$(GO) vet ./...
	$(GO) test -race ./...
	@mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/ycode ./cmd/ycode

install:
	$(GO) install -trimpath -ldflags "$(LDFLAGS)" ./cmd/ycode

benchmark: build
	./bin/ycode benchmark "provider streaming"

clean:
	$(GO) clean
	rm -f bin/ycode coverage.out
