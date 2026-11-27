#!/usr/bin/env make -f

PACKAGE_NAME	:= github.com/xmapst/taskflow
GORELEASER_VERSION	?= latest

.PHONY: all
all: copy-binary
	@cd bin && sha256sum taskflow_* > latest.sha256sum

.PHONY: dev
dev: frontend
	@CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/taskflow ./cmd

.PHONY: swag
swag:
	@swag init --parseFuncBody --dir internal/server,api --outputTypes json -g handler.go

.PHONY: frontend
frontend:
	@npm --prefix webui ci
	@npm --prefix webui run build

.PHONY: binary
binary: frontend
	@echo "Building the binary..."
	@rm -fr $(CURDIR)/dist
	@docker run \
		--rm \
		--network host \
		-v $(CURDIR):/go/src/$(PACKAGE_NAME) \
		-w /go/src/$(PACKAGE_NAME) \
		ghcr.io/goreleaser/goreleaser:${GORELEASER_VERSION} \
		release -p 3 --timeout 5h --clean --snapshot

.PHONY: copy-binary
copy-binary: binary
	@echo "Copying binaries..."
	@rm -fr $(CURDIR)/bin
	@mkdir -p $(CURDIR)/bin
	@find $(CURDIR)/dist/taskflow* -type f -not -path "*checksums*" -exec bash -c 'cp -f {} $(CURDIR)/bin/`echo {}|sed "s|$(CURDIR)/dist/||g"|sed "s|/taskflow||g"`' \;
	@rm -fr dist
