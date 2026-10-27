#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

build_in_docker() {
  local image os arch cache
  image="$(sed -n 's/^GO_BUILD_IMAGE=//p' workstation/builder.env)"
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) echo "start.sh: unsupported OS $(uname -s); install Go from https://go.dev/dl/" >&2; return 1 ;;
  esac
  case "$(uname -m)" in
    arm64 | aarch64) arch=arm64 ;;
    x86_64 | amd64) arch=amd64 ;;
    *) echo "start.sh: unsupported CPU $(uname -m); install Go from https://go.dev/dl/" >&2; return 1 ;;
  esac
  cache="${XDG_STATE_HOME:-$HOME/.local/state}/kubedrill/kubedrill/go"
  mkdir -p "$cache"
  local user=()
  if [ "$os" = linux ]; then
    user=(--user "$(id -u):$(id -g)")
  fi
  docker run --rm ${user[@]+"${user[@]}"} \
    -e HOME=/tmp -e GOCACHE=/cache/build -e GOMODCACHE=/cache/mod \
    -e GOOS="$os" -e GOARCH="$arch" -e CGO_ENABLED=0 \
    -v "$PWD:/src:ro" -v "$PWD/bin:/out" -v "$cache:/cache" -w /src \
    "$image" go build -buildvcs=false -trimpath -o /out/kubedrill ./cmd/kubedrill
}

mkdir -p bin
if command -v go >/dev/null 2>&1; then
  if ! build_log="$(go build -o bin/kubedrill ./cmd/kubedrill 2>&1)"; then
    echo "start.sh: go build failed:" >&2
    echo "$build_log" >&2
    exit 1
  fi
elif command -v docker >/dev/null 2>&1 && docker version >/dev/null 2>&1; then
  echo "go is not on PATH; building kubedrill inside a golang container (the first build also downloads the golang image)"
  if ! build_log="$(build_in_docker 2>&1)"; then
    echo "start.sh: building kubedrill with docker failed:" >&2
    echo "$build_log" >&2
    exit 1
  fi
else
  echo "start.sh: kubedrill needs a running Docker; install it from https://docs.docker.com/get-docker/, start it and run ./start.sh again." >&2
  exit 1
fi

if [ "$#" -gt 0 ]; then
  exec ./bin/kubedrill "$@"
fi

offer_path() {
  local target="$PWD/bin/kubedrill" dir="$HOME/.local/bin" link
  link="$dir/kubedrill"
  if [ "$(command -v kubedrill 2>/dev/null || true)" = "$target" ]; then
    return 0
  fi
  if [ -L "$link" ] && [ "$(readlink "$link")" = "$target" ]; then
    return 0
  fi
  case ":$PATH:" in
    *":$dir:"*)
      if { [ ! -e "$link" ] || [ -L "$link" ]; } && ln -sfn "$target" "$link" 2>/dev/null; then
        echo "kubedrill linked at ~/.local/bin/kubedrill"
        return 0
      fi
      ;;
  esac
  echo "tip: ./start.sh is all you need; to type kubedrill directly, add $PWD/bin to PATH"
}

offer_path
exec ./bin/kubedrill web
