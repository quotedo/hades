build:
	CGO_ENABLED=0 go build -o bin/ ./cmd/...

test:
	go test ./...

lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...

ci: build test lint

.PHONY: build test lint ci

release-snapshot:
	go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean

.PHONY: release-snapshot
