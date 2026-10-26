# workstation

Sources of the Docker image the learner works in. This page is for whoever
changes the image. What the learner sees, the mounts, the hardening flags
and exam mode are in [docs/workstation.md](../docs/workstation.md).

## Files

| File             | Role                                                                                     |
|------------------|------------------------------------------------------------------------------------------|
| `Dockerfile`     | Base image pinned by digest, apt packages pinned by version, every binary downloaded from its release URL and verified with `sha256sum -c` |
| `versions.env`   | The one place a tool version is written; the Dockerfile reads it                         |
| `checksums.txt`  | `<sha256>  <filename>` for every downloaded file, amd64 and arm64                        |
| `bashrc`         | Login shell: aliases, completion, prompt, episode namespace, `ssh <node>`, shell log, banner |
| `tmux.conf`      | tmux settings: mouse on, 20000 lines of history, status line with the cluster name       |
| `entrypoint.sh`  | Takes `shell` or `tmux`, builds the pane layout and starts the asciinema recording       |

The runtime behavior of `bashrc` and `entrypoint.sh` is described in
[docs/workstation.md](../docs/workstation.md): the layout and its
fallbacks, the shell log, the recording and exam mode.

## Image tag

The tag is `kubedrill-workstation:<12 hex>`, a sha256 over the six files
above. Any change here yields a new tag, and the next `kubedrill shell` or
`kubedrill start` builds it. Nothing needs cleaning by hand: older tags are
pruned after a build (rules in
[docs/workstation.md](../docs/workstation.md#image-and-binary)).

To build and try the image by hand:

```
docker build -t kubedrill-workstation:dev workstation/
docker run --rm kubedrill-workstation:dev bash -lc 'kubectl version --client && helm version'
```

A hand-made tag such as `dev` is never pruned.

## Bumping a version

1. Resolve the release:
   `gh api repos/<owner>/<repo>/releases/latest --jq .tag_name`. For
   kubectl use `curl -sL https://dl.k8s.io/release/stable-1.NN.txt`, with
   the same minor as `cluster/profiles/base.yaml`.
2. Change the line in `versions.env`.
3. Replace the lines in `checksums.txt`. Take them from the project's
   checksum file (`checksums.txt`, `checksums.sha256`, `*.sha256sum`,
   `checksums-bsd`) or from `curl -sL <url> | sha256sum`.
   - Two lines (amd64 and arm64) for kubectl, helm, k9s, stern, yq and
     asciinema. asciinema files are named
     `asciinema-<x86_64|aarch64>-unknown-linux-gnu`; the GitHub API lists
     each asset's `digest`.
   - Four lines for `KUBECTX_VERSION`: kubectx and kubens, amd64 and arm64.
   - The Dockerfile matches `<two spaces><filename>` exactly, so each file
     name must equal the one its `fetch` call writes.
4. Base image: run `docker buildx imagetools inspect debian:bookworm-slim`
   and paste the index digest into `FROM`. Re-resolve the apt versions with
   `apt-cache policy <pkg>` inside the new base.
5. Build for the host architecture and run the version command above.

CI builds the image for amd64 and arm64 with a tool smoke on pull requests
that touch these files (`.github/workflows/workstation.yml`).
