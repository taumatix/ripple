FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ripple ./cmd/ripple

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
