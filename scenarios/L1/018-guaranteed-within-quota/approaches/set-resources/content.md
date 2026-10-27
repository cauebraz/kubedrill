`kubectl set resources` writes `resources` into every container of the
Deployment's pod template, sidecars included, unless `-c` names one. Each
Deployment rolls out a new ReplicaSet whose pods declare their sizes, so
the quota can count them and admits them at once.

Choose it when each workload should own its sizing: the values live in the
Deployment, a reviewer sees them in the manifest, and different services
can get different sizes. The trade-off is drift and repetition: the next
apply of the team's manifest without `resources` removes them, so the same
values belong in the repository, and every new Deployment in the namespace
needs them too.
