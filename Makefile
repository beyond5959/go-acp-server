.PHONY: test run fmt build-web build

test:
	go test ./...

build-web:
	cd internal/webui/web && rm -rf node_modules && npm install && npm run build

build: build-web
	go build -o bin/ngent ./cmd/ngent

run: build-web
	go run ./cmd/ngent

run-local: build-web
	go run ./cmd/ngent --listen 127.0.0.1:8686 --allow-public=false

fmt:
	gofmt -w $$(find . -type f -name '*.go' -not -path './vendor/*')
