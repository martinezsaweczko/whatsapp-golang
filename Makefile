
# Makefile for building and managing the Go application
# Set LDFlags for versioning and build information
VERSION := $(shell git describe --tags --always)
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
APP_NAME := whatsappbot-golang
LDFLAGS := -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.appName=$(APP_NAME)


.PHONY: build check clean keys clean-keys

keys: clean-keys
	@mkdir -p keys
	@echo "Generating RSA private key..."
	openssl genrsa -out keys/private.pem 2048
	@echo "Generating RSA public key..."
	openssl rsa -in keys/private.pem -pubout -out keys/public.pem
	@echo "Keys generated successfully in keys/ directory"

clean-keys:
	rm -rf keys

build: clean swagger check keys
	go build -ldflags "$(LDFLAGS)" -o build/main cmd/main.go

check:
	go vet ./...
	go fmt ./...
	govulncheck ./...
	go mod tidy


clean: clean-swagger
	rm -rf build

clean-swagger:
	rm -rf docs

run: build
	./build/main

vulncheck:
	govulncheck ./...

swagger:
	mkdir -p docs && swag init -g cmd/main.go -o docs