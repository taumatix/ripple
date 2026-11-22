FROM golang:1.27 AS build
# VERSION is set by the release workflow (a tag such as v0.3.0): the binary is then built
# exactly as `go install github.com/taumatix/ripple/cmd/ripple@$VERSION` builds it, so
# `ripple version` reports the tag. Without it (docker build from a checkout) the
# working tree is built and the version reads "(devel)".
ARG VERSION=
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN set -e; \
    export CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-$(go env GOARCH)}"; \
    if [ -n "$VERSION" ]; then \
      GOPATH=/tmp/gopath GOPROXY=direct GONOSUMDB=github.com/taumatix/ripple go install -trimpath -ldflags="-s -w" "github.com/taumatix/ripple/cmd/ripple@$VERSION"; \
      mkdir -p /out && mv "$(find /tmp/gopath/bin -name ripple -type f)" /out/ripple; \
    else \
      go build -trimpath -ldflags="-s -w" -o /out/ripple ./cmd/ripple; \
    fi

# git is required to clone, commit and push; the go command is required to tidy,
# build and vet a repository after a major-version import rewrite. The final image
# is the Go toolchain image for that reason.
FROM golang:1.27-alpine
RUN apk add --no-cache git ca-certificates && adduser -D -u 10001 ripple
USER ripple
ENV GOTOOLCHAIN=local
COPY --from=build /out/ripple /usr/local/bin/ripple
EXPOSE 8080
ENTRYPOINT ["ripple"]
CMD ["serve"]
