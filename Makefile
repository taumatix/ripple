.PHONY: build vet test test-services services-up services-down
build:
	go build ./...
vet:
	go vet ./...
test:
	go test -race ./...
# Starts Postgres and DynamoDB Local with Docker; with Apple `container` use the same images and ports.
services-up:
	docker run -d --name ripple-pg -e POSTGRES_PASSWORD=ripple -p 55432:5432 postgres:17
	docker run -d --name ripple-ddb -p 58000:8000 amazon/dynamodb-local
services-down:
	-docker rm -f ripple-pg ripple-ddb
test-services:
	RIPPLE_TEST_POSTGRES_DSN='postgres://postgres:ripple@localhost:55432/postgres?sslmode=disable' \
	RIPPLE_TEST_DYNAMODB_ENDPOINT=http://localhost:58000 go test -race ./...
