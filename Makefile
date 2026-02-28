.PHONY: test run fmt build-web build

test:
	go test ./...

build-web:
	cd internal/webui/web && npm install && npm run build

build: build-web
	go build -o bin/agent-hub-server ./cmd/agent-hub-server

run: build-web
	go run ./cmd/agent-hub-server

fmt:
	gofmt -w $$(find . -type f -name '*.go' -not -path './vendor/*')
