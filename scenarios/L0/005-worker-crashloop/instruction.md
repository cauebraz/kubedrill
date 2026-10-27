# worker is CrashLoopBackOff since the last deploy

## Ticket

> **From:** Priya (billing team)\
> **Priority:** high
>
> We deployed the billing `worker` this afternoon. The deploy only touched
> the Kubernetes manifests and the config objects (we are aligning them with
> the naming convention of the other teams); nothing changed in the worker
> script, and the image is the one we have run for weeks. Since then both
> pods show `CrashLoopBackOff` and the restart count keeps growing. Invoices
> are piling up in the queue. The same script with the same config runs fine
> in staging. Can platform take a look?

## Before you start

`CrashLoopBackOff` means the container started, exited, and the kubelet is
waiting before it starts it again (10s, 20s, 40s ... up to 5 minutes). The
image and the scheduler are fine; the process itself stopped. A process that
stops on purpose says why in its last lines of output, and
`kubectl logs <pod> --previous` prints the output of the container that
exited. `kubectl describe pod` shows `Last State` with its `Exit Code`.
Read both before changing anything; the fix is one field.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `worker` (2 replicas, container `worker` running
  `sh /opt/worker/run.sh`), ConfigMap `worker-env` (the environment the
  worker reads), ConfigMap `worker-files` (the script `run.sh` and the
  config file `worker.conf`), Secret `worker-token` (the queue token)
- The worker reads the queue address and token from its environment and
  its settings from `/etc/worker/worker.conf`; it prints
  `worker started: ...` once it has all three and exits with a `worker:`
  line on stderr when it cannot start
- You have cluster-admin on the local kind cluster

## Done when

- deployment worker exists
- deployment worker has 2 ready replicas
- deployment worker has 2 replicas available for at least 5 seconds
- the worker pods log "worker started"

## Constraints

- Keep the Deployment, ConfigMap and Secret names
- Do not change the script `run.sh`: the same script runs in every environment
- Do not delete the namespace
