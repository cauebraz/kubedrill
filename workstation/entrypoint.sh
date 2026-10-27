#!/usr/bin/env bash
set -euo pipefail

export KUBEDRILL_WORKSTATION="${KUBEDRILL_WORKSTATION:-1}"
export KUBEDRILL_CLUSTER="${KUBEDRILL_CLUSTER:-kubedrill}"

recordings="${KUBEDRILL_DATA:-/data}/recordings"
user_umask=$(umask)

if [ "${KUBEDRILL_EXAM:-}" = 1 ]; then
  export PATH=/opt/exam/bin:/usr/bin:/bin
fi

has_kubedrill() { command -v kubedrill >/dev/null 2>&1; }

recording_path() {
  local ns
  [ -t 0 ] || return 1
  has_kubedrill || return 1
  [ -x /usr/local/bin/asciinema ] || return 1
  ns=$(kubedrill namespace 2>/dev/null) || return 1
  [ -n "$ns" ] || return 1
  (umask 077 && mkdir -p "$recordings") 2>/dev/null || return 1
  chmod 0700 "$recordings" 2>/dev/null || true
  printf '%s/%s-%s.cast' "$recordings" "$ns" "$(date +%s)"
}

# asciinema 3 sends OSC 10/11/4 color queries unless TERM=dumb; late answers leak into the shell.
record_command="umask \$KUBEDRILL_UMASK; exec env TERM=\"\$KUBEDRILL_TERM\" bash -l"

plain_shell() {
  local cast
  if cast=$(recording_path); then
    (umask 077 && exec env KUBEDRILL_TERM="${TERM:-xterm-256color}" KUBEDRILL_UMASK="$user_umask" TERM=dumb /usr/local/bin/asciinema rec --quiet -i 2 "$cast" -c "$record_command") || true
  else
    bash -il || true
  fi
  exit 0
}

mode="${1:-shell}"

case "$mode" in
  shell)
    plain_shell
    ;;
  *)
    exec "$@"
    ;;
esac
