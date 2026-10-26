#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

if ! command -v go >/dev/null 2>&1; then
  echo "start.sh: go is not on PATH." >&2
  echo "Install Go from https://go.dev/dl/ (version in go.mod), then run ./start.sh again." >&2
  echo "Prebuilt kubedrill releases are planned; until then the binary is built locally." >&2
  exit 1
fi

mkdir -p bin
if ! build_log="$(go build -o bin/kubedrill ./cmd/kubedrill 2>&1)"; then
  echo "start.sh: go build failed:" >&2
  echo "$build_log" >&2
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
exec ./bin/kubedrill
