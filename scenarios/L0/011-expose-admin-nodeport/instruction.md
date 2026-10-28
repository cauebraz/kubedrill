# Expose admin on NodePort 30080 for the QA laptop

## Ticket

> **From:** Ken (QA lead)\
> **Priority:** medium
>
> The admin team deployed the new back-office build (`admin`, release 3.2.0)
> to the `{{ .Namespace }}` namespace this morning and asked QA to sign it
> off today. My laptop sits on the node network, so I open things on the
> kind nodes directly at `http://<node-ip>:<port>`; platform reserved port
> 30080 for the admin sign-off. Right now `http://<node-ip>:30080/` says
> connection refused on every node, and `kubectl get svc` in the namespace
> lists nothing at all. The admin team says the pod is Running and serves
> the page fine with a port-forward, but QA has no kubectl access here. Can
> you expose `admin` so that `http://<node-ip>:30080/` shows the admin page?
{{- if eq .Vars.canary "true" }}
> One more thing: the admin team also runs their `admin-canary` build in the
> same namespace. I am signing off the stable 3.2.0 build only; do not send
> me to the canary page.
{{- end }}

## Before you start

A Service is a selector plus a port mapping. The selector picks pods by their
labels, and only Ready pods become its endpoints; `port` is the Service's own
port, `targetPort` is the port the container listens on. A Service of type
NodePort also opens `nodePort` on every node, so a client on the node network
reaches the pods at `<node-ip>:<nodePort>`. Read the pod labels and the
container port before writing the Service.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `admin` (1 replica, container `web`), ConfigMap
  `admin-html` (the page) and ConfigMap `admin-nginx` (the server config);
  no Service yet
{{- if eq .Vars.canary "true" }}
- Also in the namespace: Deployment `admin-canary` (1 replica), the admin
  team's canary build
{{- end }}
- You have cluster-admin on the local kind cluster
- The nodes are docker containers; `kubectl get nodes -o wide` prints their
  INTERNAL-IP. The QA laptop is played by a pod on the cluster network: the
  last check runs `curl http://<node-ip>:30080/` from one

## Done when

- deployment admin has 1 ready replica
- service admin exists
- service admin is of type NodePort
- service admin publishes nodePort 30080
- service admin has exactly one ready endpoint, the pod of deployment admin
- a client on the cluster network gets the ACME Admin 3.2.0 page from http://<node-ip>:30080/
- every admin Deployment in the namespace keeps its 1 replica

## Constraints

- Name the Service `admin`
- Do not change the Deployments or the ConfigMaps; the admin team owns them
- Do not delete the namespace
