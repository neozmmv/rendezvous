BINARY=rendezvous
VERSION=$(shell git describe --tags --abbrev=0)

.PHONY: build-rendezvous-win build-client-win

build-rendezvous-win:
	GOOS=windows GOARCH=amd64 go build -ldflags "-X main.Version=$(VERSION)" -o dist/$(BINARY).exe .

build-client-win:
	GOOS=windows GOARCH=amd64 go build -ldflags "-X main.Version=$(VERSION)" -o dist/p2p_client.exe ./client

build-rendezvous-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "-X main.Version=$(VERSION)" -o dist/$(BINARY)-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build -ldflags "-X main.Version=$(VERSION)" -o dist/$(BINARY)-linux-arm64 .

build-client-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "-X main.Version=$(VERSION)" -o dist/p2p_client-linux-amd64 ./client
	GOOS=linux GOARCH=arm64 go build -ldflags "-X main.Version=$(VERSION)" -o dist/p2p_client-linux-arm64 ./client

build-all: build-rendezvous-linux build-client-linux build-rendezvous-win build-client-win

build-rendezvous: build-rendezvous-linux build-rendezvous-win