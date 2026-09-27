# Makefile for building and managing the WhatsApp bot
# Set LDFlags for versioning and build information
VERSION := $(shell git describe --tags --always 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
APP_NAME := whatsappbot-golang
LDFLAGS := -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.appName=$(APP_NAME)

imageTag := whatsappbot-golang

.PHONY: build check clean keys clean-keys container run test

keys: clean-keys
	@mkdir -p keys
	@echo "Generating RSA private key..."
	openssl genrsa -out keys/private.pem 2048
	@echo "Generating RSA public key..."
	openssl rsa -in keys/private.pem -pubout -out keys/public.pem
	@echo "Keys generated successfully in keys/ directory"

clean-keys:
	rm -rf keys

build: clean check keys
	go build -ldflags "$(LDFLAGS)" -o build/main cmd/main.go

check:
	go vet ./...
	go fmt ./...
	go mod tidy

test:
	go test -race -count=1 ./...

clean:
	rm -rf build

run: build
	./build/main

vulncheck:
	govulncheck ./...

container:
	podman build -f Containerfile \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(imageTag):$(VERSION) \
		-t $(imageTag):latest \
		.
