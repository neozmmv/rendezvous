BINARY=rendezvous
VERSION=$(shell git describe --tags --abbrev=0)

.PHONY: build-rendezvous-win build-client-win

build-rendezvous-win:
	GOOS=windows GOARCH=amd64 go build -ldflags "-X main.Version=$(VERSION)" -o $(BINARY).exe .

build-client-win:
	GOOS=windows GOARCH=amd64 go build -ldflags "-X main.Version=$(VERSION)" -o p2p_client.exe ./client

build-rendezvous-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "-X main.Version=$(VERSION)" -o $(BINARY)-amd64 .
	GOOS=linux GOARCH=arm64 go build -ldflags "-X main.Version=$(VERSION)" -o $(BINARY)-arm64 .

build-client-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "-X main.Version=$(VERSION)" -o p2p_client-amd64 ./client
	GOOS=linux GOARCH=arm64 go build -ldflags "-X main.Version=$(VERSION)" -o p2p_client-arm64 ./client

build: build-rendezvous-linux build-client-linux build-rendezvous-win build-client-win