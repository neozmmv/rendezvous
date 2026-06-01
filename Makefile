BINARY=rendezvous

.PHONY: build-win build-client-win

build-win:
	GOOS=windows GOARCH=amd64 go build -o $(BINARY).exe .

build-client-win:
	GOOS=windows GOARCH=amd64 go build -o udp-client.exe ./client