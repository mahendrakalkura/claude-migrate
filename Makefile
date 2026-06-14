.PHONY: build lint

build:
	go build -o claude-migrate .

lint:
	@test -z "$$(gofmt -l .)" || (echo "unformatted files:"; gofmt -l .; exit 1)
	go vet ./...
	golangci-lint run
