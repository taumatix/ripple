# The image RIPPLE_CONTAINER_IMAGE should name for the docker, podman and container sandboxes
# (docs/sandbox.md): the go command ripple runs to tidy, build and vet a repository, and the
# git it runs locally to check out, clean and commit. GOTOOLCHAIN=local keeps the go command
# from downloading another release, so GO_VERSION must satisfy the `go` line of every
# repository ripple updates: build it with a newer one when a repository needs it.
#
#   docker build -t ripple-runner -f docker/runner/Dockerfile docker/runner
#   docker build --build-arg GO_VERSION=1.27.2 -t ripple-runner docker/runner -f docker/runner/Dockerfile
ARG GO_VERSION=1.27.1
FROM golang:${GO_VERSION}-alpine
RUN apk add --no-cache git ca-certificates
ENV GOTOOLCHAIN=local
