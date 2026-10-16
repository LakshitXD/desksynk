VERSION ?= dev
LDFLAGS = -s -w

.PHONY: all test e2e clean

all:
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -ldflags '$(LDFLAGS)' -o dist/desksynk-linux-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags '$(LDFLAGS)' -o dist/desksynk-macos-arm64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags '$(LDFLAGS)' -o dist/desksynk-macos-amd64 .

test:
	go test ./...

e2e:
	go test -tags e2e -run E2E -v -count=1 .

clean:
	rm -rf dist
