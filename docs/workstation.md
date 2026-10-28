# Workstation

The workstation is the terminal you work in. It is a Docker container with
the Kubernetes tools installed, connected to the training cluster. Like the
remote terminal of the CKA exam, it is the same environment on every
machine.

The browser workspace (`kubedrill web`) opens it in the page, next to the
ticket or the guide, for the practice, DIY and exam runs. On the host you
only need Docker. Go cross-compiles the linux `kubedrill` binary the
container uses: the host's `go` when it is on `PATH`, otherwise the golang
image of `workstation/builder.env`.

## In the page

- The terminal is xterm.js connected by a websocket to one `docker run
  --rm -it` of this image in a pseudo-terminal on the host. The bar above
  it shows the namespace, the time and the connection state.
- Select text with the mouse and copy it with the system shortcut (`Cmd-c`
  on macOS, `Ctrl-Shift-c` on most Linux terminals). TAB completes object
  names (`k describe pod <TAB>`), and a label selector names every pod of
  the app (`k describe pods -l app.kubernetes.io/name=storefront`).
- `Cmd`/`Ctrl` `=` and `-` change the text size, remembered in the browser.
- A dropped connection shows Reconnect, which starts a new container on the
  same namespace. Closing the tab removes the container; the episode and the
  cluster are untouched.
- The shell follows the episode: after a new start or a reset it switches
  to the new namespace at its next prompt.

## The shell

On login the shell:

- prints the banner `kubedrill workstation: cluster kubedrill` and `tools:
  kubectl (alias k), helm, k9s, stern, kubectx, kubens, yq, jq, vim. Type
  "kubedrill --help" for the trainer.`;
- outside exam mode, prints `names: TAB completes them (k describe pod
  <TAB>); new to Kubernetes? kubedrill story`;
- prints `ticket: the panel on the left of the page (kubedrill ticket prints
  it here)` in the browser workspace (`KUBEDRILL_WEB=1`), and the full
  ticket (`kubedrill ticket`) otherwise;
- points `kubectl` at the episode namespace, so `k get pods` needs no `-n`.

The prompt shows that namespace: `learner@kubedrill /workspace
[ns:shop-k3x9p]`.

Other settings from `workstation/bashrc`: `alias k=kubectl` with
completion, helm and `kubedrill` completion, `KUBE_EDITOR=vim`.

The learner commands of `kubedrill` work inside the workstation (`ticket`,
`check`, `hint`, `diagnose`, `story` and the rest), because the binary and
the journal are mounted; the page and the shell share the same episode.

### How the namespace is set

The kubeconfig is mounted read-only. With an active episode, the shell
copies it to `/tmp/kubeconfig` and sets the episode namespace (`kubedrill
namespace`) on the current context. A `PROMPT_COMMAND` hook compares that
namespace with `kubedrill namespace` before every prompt, so a reset is
picked up at the next prompt.

## Tools

Debian bookworm-slim (pinned by digest in `workstation/Dockerfile`), user
`learner` (uid 1000), home `/home/learner`, workdir `/workspace`.

| Tool            | Version | Source                                                  |
|-----------------|---------|---------------------------------------------------------|
| kubectl         | v1.37.1 | dl.k8s.io (same minor as `cluster/profiles/base.yaml`)  |
| helm            | v4.3.1  | get.helm.sh                                             |
| k9s             | v0.51.0 | github.com/derailed/k9s                                 |
| stern           | v1.35.0 | github.com/stern/stern                                  |
| kubectx, kubens | v0.11.0 | github.com/ahmetb/kubectx                               |
| yq              | v4.54.1 | github.com/mikefarah/yq                                 |
| asciinema       | v3.2.1  | github.com/asciinema/asciinema                          |

From Debian bookworm through apt, versions pinned in the Dockerfile: jq,
vim (vim-tiny), git, less, bash-completion, bind9-dnsutils (dig, nslookup),
ping, nc, ssh, curl, ca-certificates, procps.

## Node shells and replays

| Command                     | What it does                                                             |
|-----------------------------|--------------------------------------------------------------------------|
| `kubedrill node <name>`     | Opens a root shell on a kind node: `control-plane`, `worker` or `worker2` |
| `ssh <name>`                | Same as `kubedrill node <name>`, inside the workstation                  |
| `kubedrill replay [episode-id]` | Plays the latest recording of an episode in your terminal            |

- `node` opens a root shell on a kind node, for kubelet, containerd and
  systemd scenarios. On the host it runs `docker exec` into the node
  container. Inside the workstation there is no docker, on purpose: the
  docker socket would give root on the host and would show how kubedrill
  set up the episode. There it runs `kubectl debug node/kubedrill-<name>
  --profile=sysadmin -- chroot /host /bin/bash -l` in the `kubedrill-debug`
  namespace (outside the checks and the debrief) and deletes the pod when
  the shell ends. `up` preloads its image on the nodes.
- `ssh worker` (or `control-plane`, `worker2`, with or without the
  `kubedrill-` prefix or a `user@`) runs `kubedrill node`, also in exam
  mode, because the exam reaches nodes with `ssh <node>`. Any other `ssh`
  is the real OpenSSH client.

## What is recorded

The workstation is where kubedrill learns what you ran. The API side comes
from the cluster audit log ([study-flow.md](study-flow.md), Telemetry).
Nothing leaves your machine, and the checks never read these files.

- Shell log: one JSON line per command in `/data/shell/commands.jsonl`,
  which is `<journal dir>/shell/commands.jsonl` on the host:
  `{"ts":"2026-10-25T14:03:11.120Z","cmd":"kubectl get pods","exit":0,"ms":180}`.
  kubedrill reads it when an episode ends, to count commands and list the
  kubectl lines of "your path". Over 10 MiB it moves to
  `commands.jsonl.1` after that read.
- Recording: while an episode is active and stdin is a terminal, the shell
  runs under asciinema and writes
  `/data/recordings/<namespace>-<unix>.cast`. Idle time over 2 seconds is
  capped. `kubedrill replay` plays it.

Commands typed on the host are not in the shell log. They reach the
debrief through the audit log alone.

## Exam mode

The exam rehearsal of an assignment (the Exam rehearsal stage of the page,
or "rehearse" on the exams page) opens the exam workstation. Differences:

- The page shows the ticket only: no guide, no story, no mentor, no live
  checks, and a countdown to the deadline. Submit grades once; at the
  deadline the page submits.
- `PATH` is `/opt/exam/bin:/usr/bin:/bin`. `/opt/exam/bin` holds `kubectl`,
  `helm`, `yq`, `kubedrill` and `vim`; `jq` and the Debian tools come from
  `/usr/bin`. `k9s`, `stern`, `kubectx` and `kubens` stay in
  `/usr/local/bin`, off the `PATH`.
- The banner reads `EXAM MODE: use kubectl (k), helm, yq, jq and vim, as in
  the real exam. No hints, no solution, no live checks.` It lists the
  allowed documentation (https://kubernetes.io/docs,
  https://kubernetes.io/blog, https://helm.sh/docs,
  https://gateway-api.sigs.k8s.io) and prints the deadline with `Submit on
  the page (or kubedrill submit) when done.`

Exam mode rehearses the conditions; it is not a sandbox. A full path such
as `/usr/local/bin/k9s` still runs, and the repository, solutions included,
stays readable at `/workspace/repo` (see Accepted risks).

## Limits

- Docker is required. Without the daemon the terminal of the page prints
  the docker error in its first lines (`[kubedrill] ...`), so a permission
  problem on the docker socket shows its own text.
- The cluster must run on docker. Without the `kind` network it fails with
  `kind docker network not found; was the cluster created with docker?
  (kubedrill up)`.
- The first build downloads the base image, the apt packages and eight
  binaries (kubectl, helm, k9s, stern, kubectx, kubens, yq, asciinema). It
  needs the network once per image tag.
- The host Go must satisfy `go.mod`. The image carries no Go.
- Files written outside `/data` are gone when the session ends.
- On Linux the kind API server can write the audit log as root. The
  workstation runs with your uid and reads the log only when you can. `ls
  -l ~/.local/state/kubedrill/kubedrill/audit/audit.log` shows the owner and
  mode; an unreadable log gives `unknown` verdicts.

---

The sections below are reference for readers who want to know how the
container is built and started.

## How it is wired

`internal/workstation` starts the container with these mounts:

| Container path               | Host source                                                                 | Mode       |
|------------------------------|-----------------------------------------------------------------------------|------------|
| `/workspace/repo`            | repository root                                                             | read-only  |
| `/data`                      | journal directory (`~/.local/share/kubedrill`)                              | read-write |
| `/home/learner/.kube/config` | `<cluster state dir>/kubeconfig-internal`, written by `cluster.Manager.WriteInternalKubeconfig` | read-only |
| `/usr/local/bin/kubedrill`   | `<cluster state dir>/bin/kubedrill-linux-<arch>-<hash>`, built by `EnsureBinary` | read-only |
| `/audit`                     | cluster audit directory (`cluster.AuditDir()`, `~/.local/state/kubedrill/kubedrill/audit` by default), only when it exists | read-only |

The cluster state directory is `~/.local/state/kubedrill/kubedrill` (under
`XDG_STATE_HOME` when set), outside the journal. The journal directory is
mounted read-write, so `kubedrill` refuses to start the workstation when it
overlaps something the host later trusts: a kubeconfig or binary inside it,
or the repository or the state directory inside it, or the journal inside
either of them.

Environment:

| Variable                  | Value                                              |
|---------------------------|----------------------------------------------------|
| `KUBEDRILL_ROOT`          | `/workspace/repo`                                  |
| `KUBEDRILL_DATA`          | `/data`                                            |
| `KUBECONFIG`              | `/home/learner/.kube/config`                       |
| `KUBEDRILL_WORKSTATION`   | `1`                                                |
| `KUBEDRILL_CLUSTER`       | the cluster name                                   |
| `KUBEDRILL_NO_TERM_QUERY` | `1`: the kubedrill inside never queries the terminal for colors |
| `TERM`                    | passed through                                     |
| `KUBEDRILL_AUDIT_LOG`     | `/audit/audit.log`, when the audit directory is mounted |
| `KUBEDRILL_EXAM`, `KUBEDRILL_DEADLINE` | `1` and the deadline in RFC 3339 UTC, in exam mode |

Network: the container joins the `kind` docker network with hostname
`kubedrill`. Its kubeconfig points at `https://kubedrill-control-plane:6443`
instead of the host's `127.0.0.1` port, which is why it gets its own
kubeconfig file.

Hardening flags, on every workstation and replay container:

```
--security-opt no-new-privileges --cap-drop ALL --pids-limit 512 --memory 2g --init
```

No capability is added back. The image strips the file capability of `ping`
(`setcap -r`), which then uses an unprivileged ICMP socket
(`net.ipv4.ping_group_range`, open to every group in a docker container).

- `kubedrill replay` adds `--network none` and mounts the journal
  read-only.
- On Linux hosts both add `--user <uid>:<gid>` and `HOME=/home/learner`, so
  files written under `/data` belong to you. The image leaves
  `/home/learner` and `/home/learner/.kube` at mode 0777 so that mapped user
  can write there.

### Shell log and recording details

- The shell log comes from a DEBUG trap and a `PROMPT_COMMAND` in
  `bashrc`. The command line is taken from history
  (`HISTCONTROL=ignoredups`).
- The recording runs `asciinema rec --quiet -i 2
  /data/recordings/<namespace>-<unix>.cast -c '<restore umask and TERM>;
  exec bash -l'`. asciinema itself runs with `TERM=dumb`: with an xterm-like
  `TERM` it sends OSC 10, 11 and 4 color queries whose late answers leak
  into the shell as typed text (`]10;rgb:...`). The recorded shell gets the
  real `TERM` and umask back.
- `internal/workstation` creates `<journal dir>/shell` and
  `<journal dir>/recordings` with mode 0700 before the container starts.
  The entrypoint and `bashrc` keep both at 0700 and write the log and the
  `.cast` files with mode 0600 (umask 077). The learner's umask is otherwise
  left as it is.

### Image and binary

- Image tag: `kubedrill-workstation:<12 hex>`, a sha256 over the five image
  files in `workstation/` (Dockerfile, versions.env, checksums.txt, bashrc,
  entrypoint.sh; not `builder.env`). Changing any of them changes the tag,
  so the next terminal builds the new image. Maintainer notes are in
  [workstation/README.md](../workstation/README.md).
- `EnsureImage` runs `docker image inspect` and builds only when the tag is
  missing, with `docker build --progress quiet`. It prints `rebuilding
  workstation image <tag> (sources changed)...` when any other
  `kubedrill-workstation:<12 hex>` tag exists, whatever its age,
  `building workstation image <tag> (first time only, a few minutes)...`
  otherwise, and `workstation image
  <tag> built in <elapsed>` at the end.
- After a build it removes older `kubedrill-workstation:<12 hex>` tags and
  prints `removed <n> older workstation image(s)`. It never removes the
  current tag, a tag created within the last seven days (a parallel checkout
  may use it), untagged images or tags that are not 12 hex digits (such as
  `dev`).
- The docker queries (`info`, `image inspect`, `image ls`, `network
  inspect`, `inspect`) give up after 10 seconds and keep docker's error
  text. The build and the interactive sessions have no timeout; cancelling
  the build kills its whole process group.
- A session that ends with status 126 or 127 counts as a docker failure
  only when docker printed a message; otherwise it is the status of your
  last shell command and is not reported.
- `EnsureBinary` cross-compiles `cmd/kubedrill` with `GOOS=linux
  CGO_ENABLED=0 -trimpath -buildvcs=false` for the host architecture on
  every run; the Go build cache makes an unchanged tree a no-op of about
  0.2 s, and a deleted source file is never left in a stale binary. It then
  removes the binaries of other checkouts last built more than 7 days ago.
- Without `go` on `PATH`, `EnsureBinary` prints `go is not on PATH; building
  kubedrill for the workstation inside <image>` and runs the same build in
  `GO_BUILD_IMAGE` of `workstation/builder.env` (pinned by digest, its Go
  version equal to `go.mod`, which a test checks), with the repository
  mounted read-only and the module and build caches under `<state dir>/go`.
  `start.sh` reads the same file.
- `kubedrill web` calls `Prepare`, which runs `EnsureImage` and
  `EnsureBinary` while the cluster is created, so the first terminal opens
  without a second wait.

### Entrypoint

`workstation/entrypoint.sh` takes `shell` (the default): one login shell,
recorded by asciinema while an episode is active; any other argument runs
as a command (`kubedrill replay` uses this). In exam mode it sets the exam
`PATH` first. The container exits 0 when the shell ends.

## Accepted risks

- Read-only mounts protect against mistakes, not against a learner with host
  access. The internal kubeconfig and the linux binary live in the cluster
  state directory, outside `/data`, and are mounted read-only, so a process
  in the container cannot edit what the next terminal mounts. The
  `:ro` mounts stop an `rm -rf` in the wrong directory or a `kubectl config`
  write.
- `/data` is read-write. A process in the container can edit the shell log
  and the recordings, which only changes your own debrief.
- The kubeconfig holds only the `kind-kubedrill` context, so the exposure is
  the local cluster.
- The repository is readable at `/workspace/repo` in every mode, exam
  included, so `solution/` and `checks.yaml` of the running scenario can be
  read from the shell. Exam mode does not hide the answer from a learner who
  looks for it.
- The `gateway` addon (`cluster/addons/gateway/addon.yaml`) runs
  `cloud-provider-kind` as a docker container with `/var/run/docker.sock`
  mounted, because it assigns LoadBalancer addresses through the docker
  daemon that runs the kind nodes. That container has the same power over
  the host's docker as you. It is an upstream requirement, declared with
  `host_access: true` (the addon loader refuses a host path without it),
  pinned by image digest, started only by `kubedrill up --addons gateway`,
  and not reachable
  from the workstation container.
