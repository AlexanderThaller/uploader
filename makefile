all:
	make format
	make test
	make build

format:
	gofmt -s=true -w=true *.go
	goimports -w=true *.go

test:
	go test -test.v=true ./...

build:
	go build -ldflags "-X main.buildtime `date +%s` -X main.version `git describe --always`"
