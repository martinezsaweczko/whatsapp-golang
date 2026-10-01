# Makefile for building and managing the WhatsApp bot
# Set LDFlags for versioning and build information
VERSION := $(shell git describe --tags --always 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
APP_NAME := whatsappbot-golang
LDFLAGS := -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.appName=$(APP_NAME)

imageTag := whatsappbot-golang
SWAG := $(shell go env GOPATH)/bin/swag
GOOSE := $(shell go env GOPATH)/bin/goose
DB_DRIVER ?= sqlite
DB_DSN ?= /tmp/db.sqlite

.PHONY: build binary check check-swagger clean clean-keys container keys migrate migrate-mysql run swagger test vulncheck

keys: clean-keys
	@mkdir -p keys
	@echo "Generating RSA private key..."
	openssl genrsa -out keys/private.pem 2048
	@echo "Generating RSA public key..."
	openssl rsa -in keys/private.pem -pubout -out keys/public.pem
	@echo "Keys generated successfully in keys/ directory"

clean-keys:
	rm -rf keys

swagger:
	$(SWAG) init -g cmd/main.go -o docs

migrate:
ifeq ($(DB_DRIVER),sqlite)
	$(GOOSE) -dir repository/migrations/sqlite sqlite3 "$(DB_DSN)" up
else ifeq ($(DB_DRIVER),mysql)
	$(GOOSE) -dir repository/migrations/mysql mysql "$(DB_DSN)" up
else
	@echo "Unsupported DB_DRIVER=$(DB_DRIVER). Use sqlite or mysql."
	@exit 1
endif

migrate-mysql:
	$(GOOSE) -dir repository/migrations/mysql mysql "$(MYSQL_DSN)" up

check-swagger:
	@rm -rf /tmp/docs
	$(SWAG) init -g cmd/main.go -o /tmp/docs
	@diff -r docs /tmp/docs || (echo "Swagger docs are out of date. Run 'make swagger' and commit the changes." && exit 1)

build: clean check check-swagger keys
	go build -ldflags "$(LDFLAGS)" -o build/main cmd/main.go

binary: swagger
	mkdir -p /tmp/periodico/nacional
	mkdir -p /tmp/periodico/internacional
	mkdir -p /tmp/periodico/magazine
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
