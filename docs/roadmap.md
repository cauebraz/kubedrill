# Roadmap

Where the curriculum goes and what the platform still lacks.

- Today: 1 scenario exists (`001-storefront-down`, L0).
- Planned: 138 scenarios in 40 modules across 7 levels, 575 hours in total.

Everything here is read from `curriculum.yaml`. `kubedrill path` prints the
ladder with your progress, and `kubedrill exam <CKA|CKAD|CKS>` prints the
exam coverage.

## The ladder

| Level | Title                 | Hours | Planned scenarios |
|-------|-----------------------|-------|-------------------|
| L0    | Foundations           | 25    | 14                |
| L1    | Application operator  | 60    | 21                |
| L2    | Cluster operator      | 90    | 32                |
| L3    | Security engineer     | 80    | 19                |
| L4    | Platform builder      | 120   | 23                |
| L5    | Scale and reliability | 120   | 19                |
| L6    | Staff                 | 80    | 10                |

Each level below lists who it is for, what you can do after it, the
capstone that closes it and its modules.

### L0 Foundations

- Before: has used Docker, never kubectl.
- After: reads any object, explains the pod lifecycle, fixes one-object
  failures from describe and events.
- Capstone: fix 5 broken apps in 30 minutes.

| Module | Title                     | Planned |
|--------|---------------------------|---------|
| L0.1   | Reading the cluster       | 4       |
| L0.2   | Pod lifecycle             | 4       |
| L0.3   | Services and labels       | 3       |
| L0.4   | Config and secrets basics | 3       |

### L1 Application operator

- Before: deploys apps given manifests.
- After: authors Deployments, Jobs, probes, config and volumes correctly and
  debugs an app in a namespace without help.
- Capstone: ship a 3-tier app from manifests with probes, quotas and a
  rollback drill.

| Module | Title                                    | Planned |
|--------|------------------------------------------|---------|
| L1.1   | Deployments and rollouts                 | 4       |
| L1.2   | Jobs, CronJobs, StatefulSets, DaemonSets | 4       |
| L1.3   | Resources, QoS, autoscaling basics       | 3       |
| L1.4   | Application configuration and storage    | 4       |
| L1.5   | Observability for app developers         | 3       |
| L1.6   | Helm and Kustomize as a consumer         | 3       |

### L2 Cluster operator

- Before: on-call for a namespace.
- After: on-call for a cluster: scheduling, Services and DNS, Ingress,
  storage, RBAC, node operations, upgrades, etcd backup.
- Capstone: rebuild the cluster from an etcd backup, then pass every L2.7
  drill.

| Module | Title                          | Planned |
|--------|--------------------------------|---------|
| L2.1   | Scheduling                     | 5       |
| L2.2   | Networking                     | 6       |
| L2.3   | Storage                        | 4       |
| L2.4   | Control plane, kubeadm, etcd   | 5       |
| L2.5   | Node operations                | 4       |
| L2.6   | RBAC and access                | 4       |
| L2.7   | Cluster troubleshooting drills | 4       |

### L3 Security engineer

- Before: passes CKA.
- After: hardens a cluster end to end: admission, Pod Security, supply
  chain, runtime, audit; passes CKS.
- Capstone: kube-bench clean, signed images only, and an audit-log drill.

| Module | Title                       | Planned |
|--------|-----------------------------|---------|
| L3.1   | Cluster setup and hardening | 3       |
| L3.2   | Pod Security and admission  | 4       |
| L3.3   | RBAC and identity hardening | 3       |
| L3.4   | Supply chain                | 3       |
| L3.5   | Runtime security and audit  | 3       |
| L3.6   | Network security            | 3       |

### L4 Platform builder

- Before: runs one cluster.
- After: builds the platform layer: GitOps, policy as code, observability,
  autoscaling, tenancy, operators.
- Capstone: bootstrap a platform from Git: ArgoCD manages itself, policies,
  monitoring and one tenant.

| Module | Title                             | Planned |
|--------|-----------------------------------|---------|
| L4.1   | GitOps                            | 4       |
| L4.2   | Policy as code and tenancy        | 3       |
| L4.3   | Observability platform            | 4       |
| L4.4   | Autoscaling and capacity          | 3       |
| L4.5   | Operators and CRDs                | 4       |
| L4.6   | Service mesh and advanced traffic | 3       |
| L4.7   | Developer experience              | 2       |

### L5 Scale and reliability

- Before: owns a platform for a handful of teams.
- After: runs 20k+ pods: API server and etcd capacity, controllers at scale,
  incident command, SLOs, multi-cluster.
- Capstone: run the Sev1 drill and write the postmortem.

| Module | Title                              | Planned |
|--------|------------------------------------|---------|
| L5.1   | API server and etcd at scale       | 4       |
| L5.2   | Scheduler and controllers at scale | 3       |
| L5.3   | Networking at scale                | 3       |
| L5.4   | Upgrades, DR, multi-cluster        | 4       |
| L5.5   | Incident command                   | 3       |
| L5.6   | Cost and efficiency                | 2       |

### L6 Staff

- Before: senior platform engineer.
- After: sets direction: design reviews, capacity plans, migration strategy,
  postmortems that change the organization, cost.
- Capstone: contribute one scenario from a real incident you lived through.

| Module | Title                               | Planned |
|--------|-------------------------------------|---------|
| L6.1   | Design reviews                      | 3       |
| L6.2   | Fleet capacity planning             | 2       |
| L6.3   | Postmortems and reliability program | 2       |
| L6.4   | Migration and strategy              | 3       |

## Scenario types and feasibility

Every planned scenario in `curriculum.yaml` has a ticket, a type and a
feasibility.

| Type        | What you do                                          |
|-------------|------------------------------------------------------|
| `break-fix` | Repair a deployed fault                              |
| `build`     | Create something new                                 |
| `incident`  | A timed drill with several simultaneous faults       |
| `design`    | A written answer graded by a structural rubric       |

| Feasibility | Runs on                                                                   |
|-------------|---------------------------------------------------------------------------|
| `kind`      | The base profile                                                          |
| `addon`     | The base profile plus listed add-ons, for example `cilium`, `kube-prometheus`, `argocd` |
| `simulated` | KWOK nodes and pods (the `kwok` addon)                                    |

A scenario that exists gets its `id` in `curriculum.yaml`. That is how
`kubedrill path` counts existing and solved scenarios per module.

### Scale scenarios on KWOK

Scenarios marked `simulated` are mostly L5, plus one in L4 and one in L6.
`kubedrill up --addons kwok` installs the KWOK v0.8.0 controller with its
fast stages.

- Real: the API server, etcd, the scheduler and the controllers.
- Simulated: thousands of nodes and pods, run by a controller instead of
  kubelets.
- Exercised: LIST and watch pressure, etcd size, API Priority and Fairness,
  scheduler throughput, DaemonSet rollout limits.
- Not exercised: image pulls, container restarts, probes and the network
  datapath. Scenarios that need them stay on `kind` or `addon`.

### Design scenarios

A `design` scenario (an RFC review, a tenancy decision, a capacity plan, a
postmortem) produces a written answer instead of a cluster state. The
planned grader checks structure: required sections, named failure modes,
numbers present, trade-offs stated. `scenario.toml` accepts the type today,
but no rubric grader exists yet, so no `design` scenario ships.

## Exam map

Each module lists the exam domains it touches as `EXAM:domain`.
`kubedrill exam CKA` prints done/total per domain. L0 to L2 cover the CKA
and CKAD domains and L3 covers CKS. Later levels keep tagging domains, so
the coverage table stays meaningful after the exams.

| Exam | Domain                                               | Weight |
|------|------------------------------------------------------|--------|
| CKA  | Cluster Architecture, Installation and Configuration | 25%    |
| CKA  | Workloads and Scheduling                             | 15%    |
| CKA  | Services and Networking                              | 20%    |
| CKA  | Storage                                              | 10%    |
| CKA  | Troubleshooting                                      | 30%    |
| CKAD | Application Design and Build                         | 20%    |
| CKAD | Application Deployment                               | 20%    |
| CKAD | Application Observability and Maintenance            | 15%    |
| CKAD | Application Environment, Configuration and Security  | 25%    |
| CKAD | Services and Networking                              | 20%    |
| CKS  | Cluster Setup                                        | 15%    |
| CKS  | Cluster Hardening                                    | 15%    |
| CKS  | System Hardening                                     | 10%    |
| CKS  | Minimize Microservice Vulnerabilities                | 20%    |
| CKS  | Supply Chain Security                                | 20%    |
| CKS  | Monitoring, Logging and Runtime Security             | 20%    |

## Platform roadmap

Already done, documented elsewhere:

- Per-episode namespaces with background GC.
- The API audit log in the base profile.
- Add-ons `metrics-server` (installed by every `up`), `gateway` (Envoy
  Gateway v1.9.2 with cloud-provider-kind v0.12.0) and `kwok`.
- Node faults (`kubedrill fault`, `heal`).
- Spaced review in the Next recommendation.
- Single-scenario exam mode (`start --exam`, `submit`).

Still to do:

- Prebuilt binaries: `kubedrill` for macOS and Linux via GoReleaser (a
  `.goreleaser.yaml` and a tag-triggered release workflow), so Go is no
  longer required. `start.sh` says so when `go` is missing.
- Cluster-scoped objects in scenarios: ClusterRoles, CRDs, PriorityClasses
  and node labels, declared in `scenario.toml` with their cleanup, so a reset
  removes them with the namespace.
- More cluster profiles under `cluster/profiles/`: `cilium` (NetworkPolicy
  and eBPF datapath) and `multi` (a second cluster for failover). The
  `profile` key in `scenario.toml` is parsed but not acted on yet.
- Add-ons on demand before a scenario starts. The `addons` and `requires`
  keys of `scenario.toml` are parsed but not acted on. The add-ons
  `curriculum.yaml` names beyond the three shipped (cert-manager, kyverno,
  kube-prometheus, argocd, gitea, keda, vpa, velero and others) have no
  directory under `cluster/addons/` yet. `curriculum.yaml` writes
  `envoy-gateway` and `cloud-provider-kind` for what the `gateway` addon
  installs together.
- Capstone exams: several scenarios under one clock, for example "fix 5
  broken apps in 30 minutes". `--exam` times one scenario today.
- Rubric grading for `design` scenarios.
- pt-BR translations of tickets, hints and solutions, once the scenario set
  stabilizes.

## What a local lab cannot teach

- Organizational influence: convincing a team to adopt a policy, running a
  design review with people who disagree, negotiating an exception process.
  L6 `design` scenarios rehearse the written artifact, not the room.
- Real on-call stress: a kind cluster has no pager, no customers and no
  escalation. Incident scenarios time-box the drill, but the cost of being
  wrong is a reset.
- Cloud provider behavior: load balancers, IAM, managed node pools and
  managed control planes are stand-ins here (`cloud-provider-kind`, local
  registries, KWOK).
- Scale beyond simulation: KWOK reproduces control plane load, not kubelet,
  CNI or storage behavior at 500 nodes.
- Months-long effects: certificate expiry, etcd growth and upgrade cadence
  are compressed into a scenario setup. The lab shows the mechanism, not the
  waiting.
