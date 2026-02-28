.PHONY: test run fmt

test:
	go test ./...

run:
	go run ./cmd/agent-hub-server

fmt:
	gofmt -w $$(find . -type f -name '*.go' -not -path './vendor/*')
