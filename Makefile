.PHONY: build test race vet fmt fmt-check lint check clean

build:
	go build -o bin/nyrvo ./cmd/nyrvo

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || { echo "gofmt found unformatted files:"; gofmt -l .; exit 1; }

# `go install` puts golangci-lint in GOPATH/bin, which is often not on PATH, so
# look there too before declaring it missing.
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	elif [ -x "$$(go env GOPATH)/bin/golangci-lint" ]; then \
		"$$(go env GOPATH)/bin/golangci-lint" run ./...; \
	else \
		echo "golangci-lint not installed; skipping lint"; \
	fi

check: test race vet fmt-check lint

clean:
	rm -rf bin/
