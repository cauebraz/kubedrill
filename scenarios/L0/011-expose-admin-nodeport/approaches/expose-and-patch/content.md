`kubectl expose deployment admin` creates a Service whose selector is the
Deployment's `spec.selector.matchLabels`, so it matches exactly the pods the
Deployment manages and no sibling workload. `--type=NodePort` opens a node
port, and `--target-port` names the container port; without `--port` and
`--target-port` kubectl copies the first container port into both, which is
also correct here. `expose` has no flag for the node port, so the API server
allocates a random one and the `patch` replaces it with 30080.

Choose it during a sign-off or an incident: two commands, no file, and the
selector comes from the Deployment, so the canary problem cannot happen.

The trade-off is drift and two steps. The Service exists only in the cluster
until someone exports it to the repository, and between the `expose` and the
`patch` the page is reachable on a random port, which matters when a runbook
or a firewall rule already names 30080. Export it afterwards with
`kubectl get service admin -o yaml` and commit the file.
