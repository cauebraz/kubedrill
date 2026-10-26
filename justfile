set shell := ["bash", "-euo", "pipefail", "-c"]

default: check

# gofmt, vet, race tests, build, tidy check, shellcheck and scenario lint
check: fmt-check vet test build tidy-check shellcheck lint-scenarios

fmt-check:
    test -z "$(gofmt -l .)" || { gofmt -l .; echo "gofmt: files above need formatting"; exit 1; }

fmt:
    gofmt -w .

vet:
    go vet ./...

test:
    go test ./... -race -count=1

build:
    mkdir -p bin
    go build -o bin/kubedrill ./cmd/kubedrill

tidy-check:
    go mod tidy -diff

shellcheck:
    shellcheck -x start.sh workstation/entrypoint.sh workstation/bashrc $(find scenarios -name '*.sh')

lint-scenarios: build
    ./bin/kubedrill lint

# Create the kind cluster
cluster: build
    ./bin/kubedrill up

# govulncheck over the module; needs network, so it is not part of check
vuln:
    go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

# check, vuln and every scenario end to end against the current cluster (CI equivalent)
check-ci: check vuln smoke

smoke *scenarios: build
    ./bin/kubedrill smoke {{scenarios}}

# Scaffold a new scenario: just new-scenario L0 011 my-slug
new-scenario level id slug: build
    ./bin/kubedrill new {{level}} {{id}}-{{slug}}
