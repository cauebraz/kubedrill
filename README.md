# kubedrill

Practice Kubernetes on the tickets platform and SRE teams get at work.

Each scenario breaks something in a kind cluster on your machine. You get a
ticket, you fix it with `kubectl`, and `kubedrill` grades the end state. Any
fix that works passes. No slides, no multiple choice.

## Quick start

You need Docker running, `kubectl`, and Go 1.27.3 on `PATH`. kind is built
into the binary, so there is nothing else to install.

```sh
git clone https://github.com/cauebraz/kubedrill.git && cd kubedrill && ./start.sh
```

That is the only command to remember. `./start.sh` builds `kubedrill`,
checks your tools, creates a local cluster (Kubernetes v1.37.0, one control
plane, two workers) and offers your first scenario. Press Enter and the
workstation opens. Next time, run `./start.sh` again: it reopens the
scenario you were on, or offers the next one.

The first run downloads the kind node image (about 1 GB). The first
`kubedrill start` also builds the workstation image (about 800 MB). Both
take a few minutes, once. `kubedrill up --addons gateway,kwok` adds Envoy
Gateway and KWOK to the cluster.

## Your first scenario

The workstation opens in your terminal:

```
+------------------------------+------------------+
| the ticket (scroll it with   | live checks      |
| the mouse wheel)             | (kubedrill watch)|
+------------------------------+                  |
| your shell, already in the   |                  |
| scenario namespace           |                  |
+------------------------------+------------------+
```

Read the ticket, investigate with `kubectl`, fix it. The checks on the right
turn to `[pass]` as you go. When you are stuck:

```sh
kubedrill hint                   # the next hint: where to look, what to observe, what it means
kubedrill diagnose "<cause>"     # name the cause in one line; the last hint and the solution wait for it
kubedrill check                  # grade once and see expected vs actual for each check
```

After a solve, kubedrill compares your diagnosis with the real root cause.
It asks which observation would have ruled out your first guess sooner.
Then `kubedrill list` shows the next scenario.

The method behind every scenario is one loop: describe the symptom, list
candidate causes, find the observation that splits them, change one thing,
verify. `kubedrill learn troubleshooting-loop` explains it.

## Documentation

| Read this                                            | When you want to                                              |
|------------------------------------------------------|---------------------------------------------------------------|
| [docs/study-flow.md](docs/study-flow.md)             | Understand grading, hints, the debrief, mastery and exam mode |
| [docs/workstation.md](docs/workstation.md)           | Learn the workstation panes, tools and recording              |
| [docs/cli.md](docs/cli.md)                           | Look up a command, flag, message or exit code                 |
| [docs/concepts/](docs/concepts/README.md)            | Read about a mechanism a ticket uses                          |
| [docs/roadmap.md](docs/roadmap.md)                   | See the levels L0 to L6 and the planned scenarios             |
| [CONTRIBUTING.md](CONTRIBUTING.md)                   | Write a scenario                                              |
| [docs/scenario-format.md](docs/scenario-format.md)   | Look up a scenario file or field                              |
| [docs/design.md](docs/design.md)                     | Know why it works this way, with sources                      |

## Your data stays local

Your progress lives in `~/.local/share/kubedrill`. The workstation records
your shell commands and the cluster keeps an API audit log. kubedrill uses
them only for the debrief. Nothing is sent anywhere, and the grade never
reads them. Details are in
[docs/study-flow.md](docs/study-flow.md#telemetry).

## Contributing

New scenarios are welcome, especially ones you lived through. Start with
[CONTRIBUTING.md](CONTRIBUTING.md). Run `just check` before a pull request.

## License

[MIT](LICENSE), except the upstream manifests vendored in `cluster/addons/`,
which keep their [Apache-2.0](LICENSES/Apache-2.0.txt) license
([details](cluster/addons/README.md)).
