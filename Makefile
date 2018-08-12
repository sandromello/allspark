SHORT_NAME ?= allspark

include versioning.mk

# # It's necessary to set this because some environments don't link sh -> bash.
SHELL := /bin/bash

# Common flags passed into Go's linker.
GOTEST := go test --race -v

LDFLAGS := "-s -w \
-X github.com/sparkcorp/allspark/pkg/version.version=${VERSION} \
-X github.com/sparkcorp/allspark/pkg/version.gitCommit=${GITCOMMIT} \
-X github.com/sparkcorp/allspark/pkg/version.buildDate=${DATE}"

BINARY_DEST_DIR := rootfs/usr/local/bin

GOOS ?= linux
GOARCH ?= amd64

test:
	${GOTEST} ./pkg/...

build: clean
	env GOOS=${GOOS} GOARCH=${GOARCH} CGO_ENABLED=0 go build -ldflags ${LDFLAGS} -o ${BINARY_DEST_DIR}/ini-sync cmd/syncer/syncer.go
	env GOOS=${GOOS} GOARCH=${GOARCH} CGO_ENABLED=0 go build -ldflags ${LDFLAGS} -o ${BINARY_DEST_DIR}/allspark-controller-manager cmd/allspark/allspark.go

docker-release:
	upx rootfs/usr/local/bin/* || true
	docker build --build-arg RELEASE=${FRP_VERSION} -t ${REGISTRY}/${IMAGE_PREFIX}/${SHORT_NAME}:${VERSION} ./rootfs
	docker push ${REGISTRY}/${IMAGE_PREFIX}/${SHORT_NAME}:${VERSION}

clean:
	rm -f ${BINARY_DEST_DIR}/*

.PHONY: build test clean