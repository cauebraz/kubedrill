#!/usr/bin/env bash
set -euo pipefail

export KUBEDRILL_WORKSTATION="${KUBEDRILL_WORKSTATION:-1}"
export KUBEDRILL_CLUSTER="${KUBEDRILL_CLUSTER:-kubedrill}"

min_columns=120
min_ticket_lines=30
session=kubedrill
recordings="${KUBEDRILL_DATA:-/data}/recordings"
user_umask=$(umask)

exam=0
if [ "${KUBEDRILL_EXAM:-}" = 1 ]; then
  exam=1
  export PATH=/opt/exam/bin:/usr/bin:/bin
fi

has_kubedrill() { command -v kubedrill >/dev/null 2>&1; }
episode_active() { has_kubedrill && kubedrill namespace >/dev/null 2>&1; }

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

recorded_shell_command() {
  local cast
  if cast=$(recording_path); then
    # shellcheck disable=SC2016
    printf 'umask 077; exec env KUBEDRILL_TERM="$TERM" KUBEDRILL_UMASK=%s TERM=dumb /usr/local/bin/asciinema rec --quiet -i 2 %s -c %s' \
      "$user_umask" "$cast" "'$record_command'"
  else
    printf 'exec bash -l'
  fi
}

terminal_columns() {
  local cols
  cols=$(tput cols 2>/dev/null || true)
  case "$cols" in
    ''|*[!0-9]*) cols=$(stty size 2>/dev/null | awk '{print $2}' || true) ;;
  esac
  case "$cols" in
    ''|*[!0-9]*) cols=0 ;;
  esac
  printf '%s' "$cols"
}

terminal_lines() {
  local lines
  lines=$(tput lines 2>/dev/null || true)
  case "$lines" in
    ''|*[!0-9]*) lines=$(stty size 2>/dev/null | awk '{print $1}' || true) ;;
  esac
  case "$lines" in
    ''|*[!0-9]*) lines=0 ;;
  esac
  printf '%s' "$lines"
}

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
  tmux)
    if ! has_kubedrill; then
      echo "kubedrill binary not mounted at /usr/local/bin/kubedrill; starting a plain shell" >&2
      plain_shell
    fi
    if [ "$(terminal_columns)" -lt "$min_columns" ]; then
      echo "terminal narrower than ${min_columns} columns; starting a plain shell (kubedrill watch runs in a second terminal)"
      plain_shell
    fi
    lines=$(terminal_lines)
    [ "$lines" -gt 0 ] || lines=50
    active=0
    episode_active && active=1
    ticket_pane=0
    if [ "$active" = 1 ] && [ "$lines" -ge "$min_ticket_lines" ]; then
      ticket_pane=1
    fi
    shell_pane=$(tmux -f /etc/kubedrill/tmux.conf new-session -d -P -F '#{pane_id}' -s "$session" -n work -x "$(terminal_columns)" -y "$lines" -e KUBEDRILL_TICKET_PANE="$ticket_pane" "$(recorded_shell_command)")
    tmux set-environment -t "$session" KUBEDRILL_MOTD_SHOWN 1
    tmux set-hook -t "$session" pane-exited "if-shell -F '#{==:#{hook_pane},$shell_pane}' 'kill-session -t $session'"
    if [ "$exam" = 0 ] && [ "$active" = 1 ]; then
      tmux split-window -t "$shell_pane" -h -l 40% "kubedrill watch; exec bash -l"
    fi
    if [ "$ticket_pane" = 1 ]; then
      tmux split-window -t "$shell_pane" -v -b -l 45% "kubedrill ticket --follow; exec bash -l"
    fi
    tmux select-pane -t "$shell_pane"
    tmux attach-session -t "$session" || true
    exit 0
    ;;
  *)
    exec "$@"
    ;;
esac
