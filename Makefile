GO   ?= go
MINT ?= https://testnut.cashudevkit.org

.PHONY: build test vet smoke run tidy clean

build:
	$(GO) build -o bin/cashu ./cmd/cashu

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

smoke:
	$(GO) run ./cmd/cashu smoke --mint $(MINT)

tidy:
	$(GO) mod tidy

clean:
	rm -rf bin
