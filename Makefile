# Canonical target set — see ~/.claude/CLAUDE.md. Recipes are tailored to
# nitro: a single-binary Go server that is both a deploy artifact
# (linux/arm64 behind Docker) and a dev tool (macOS, Windows).

BINARY  := nitro
DIST    := dist
# -trimpath and -s -w: reproducible paths and a stripped binary; nitro
# ships as a bare artifact, so debug info stays on the dev machine.
GOFLAGS := -trimpath -ldflags='-s -w'

.PHONY: build test run release clean docker-build deploy logs

build:
	go build -o $(BINARY) .

test:
	go vet ./...
	go test ./...

run: build
	./$(BINARY)

release:
	rm -rf $(DIST)
	mkdir -p $(DIST)
	# CGO_ENABLED=0 keeps every artifact static — no libc dependency on
	# the deploy target.
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -o $(DIST)/$(BINARY)-linux-arm64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o $(DIST)/$(BINARY)-linux-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -o $(DIST)/$(BINARY)-darwin-arm64 .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -o $(DIST)/$(BINARY)-windows-amd64.exe .

clean:
	rm -rf $(BINARY) $(DIST)

docker-build:
	docker compose build

deploy:
	git pull
	docker compose up -d --build

logs:
	docker compose logs -f nitro
