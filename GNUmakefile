default: build

.PHONY: build install test testacc lint docs fmt

build:
	go build ./...

install:
	go install .

test:
	go test ./... -count=1

testacc:
	TF_ACC=1 go test ./... -run TestAcc -timeout 120m

lint:
	golangci-lint run

docs:
	go generate ./...

fmt:
	gofmt -s -w .
