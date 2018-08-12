GIT_TAG ?= $(or ${TRAVIS_TAG},${TRAVIS_TAG},latest)
MUTABLE_VERSION ?= latest
VERSION ?= ${GIT_TAG}
GITCOMMIT ?= $(shell git rev-parse HEAD)
DATE ?= $(shell date -u "+%Y-%m-%dT%H:%M:%SZ")

REGISTRY ?= quay.io
IMAGE_PREFIX ?= sandromello

IMAGE := ${REGISTRY}/${IMAGE_PREFIX}/${SHORT_NAME}:${VERSION}
MUTABLE_IMAGE := ${REGISTRY}/${IMAGE_PREFIX}/${SHORT_NAME}:${MUTABLE_VERSION}

info:
	@echo "Build tag:       ${VERSION}"
	@echo "Registry:        ${REGISTRY}"
	@echo "Immutable tag:   ${IMAGE}"
	@echo "Mutable tag:     ${MUTABLE_IMAGE}"

docker-push: docker-login
	docker push ${REGISTRY}/${IMAGE_PREFIX}/allspark:${VERSION}

docker-login:
	docker login ${REGISTRY} -u="${DOCKER_USERNAME}" -p="${DOCKER_PASSWORD}"

.PHONY: docker-push docker-login

