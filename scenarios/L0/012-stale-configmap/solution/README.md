# Solution - 012 App reads old config after we updated the ConfigMap

## Symptom

`http://banner.{{ .Namespace }}.svc` returns `Summer sale 2025` twenty
minutes after the team changed the key `banner` of ConfigMap
`{{ .Vars.edited }}` to `Winter sale 2026`. Both pods of Deployment `banner`
are Running and Ready, nothing restarted, and `kubectl get configmap
{{ .Vars.edited }} -o yaml` prints the new text. The signal the reporter
trusted, the API showing the new value, proves the write happened; it says
nothing about whether a running container ever read it.

## Candidate causes

A fresh ConfigMap in the API with a stale page fits four causes, each with
its own trace:

- The container reads the value through an env var
  (`valueFrom.configMapKeyRef`): the kubelet resolves it once at container
  start, so `kubectl exec <pod> -- env` prints the old value and the pods are
  older than the edit.
- The ConfigMap is mounted with `subPath`: the kubelet bind-mounts one file
  at pod creation and never refreshes it, so `cat` inside the container
  prints the old text although the pod template has no env var.
- The Deployment names a different ConfigMap than the one the team edited:
  `kubectl get configmaps` lists more than one, and the volume in the pod
  template references the one that still holds the old text.
- A cache in front of the pods (a CDN, an Ingress cache, the browser): the
  pods themselves serve the new text, and only the edge is stale. The ticket
  rules this out, the reporter curled the Service from a pod.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get configmaps
kubectl -n {{ .Namespace }} get configmaps -o yaml
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} exec deploy/banner -- env
kubectl -n {{ .Namespace }} exec deploy/banner -- cat /usr/share/nginx/html/index.html
kubectl -n {{ .Namespace }} get deployment banner -o yaml
```

`exec deploy/banner` picks one pod of the Deployment; a pod name from the
NAME column of `get pods` works too. The first two commands show what the
API holds; the two `exec` commands show what the container holds; the
Deployment shows the route between them. The container prints
`Summer sale 2025` in every variant, so the edit never reached it, and the
pod template says why (measured on kind v1.37):

- env-var: `env` prints `BANNER_TEXT=Summer sale 2025`, and the pod template
  has `env[0].valueFrom.configMapKeyRef` with `name: banner-config` and
  `key: banner`. The `args` line writes `index.html` from `$BANNER_TEXT`
  when the container starts. The pods' AGE is older than the edit, which
  `kubectl get configmap banner-config -o yaml --show-managed-fields` dates
  in the `time` of the `kubectl-patch` entry.
- subpath-mount: `env` has no `BANNER_TEXT`; `cat` prints `Summer sale
  2025`, and the `volumeMounts` entry reads `mountPath:
  /usr/share/nginx/html/index.html` with `subPath: index.html`. A subPath
  mount is a bind mount of the file as it was at pod creation; the kubelet
  updates the `..data` symlink of a whole-ConfigMap volume but never a
  subPath.
- wrong-name: `get configmaps` lists `banner-config` and
  `banner-config-v2`; the `-o yaml` output shows `Winter sale 2026` only in
  `banner-config-v2`, and the pod template's volume reads `configMap: {name:
  banner-config}`. The edit went to an object no pod reads.

## Change

For the env var and the subPath mount, replace the pods; each new pod reads
the ConfigMap when it starts:

```sh
kubectl -n {{ .Namespace }} rollout restart deployment/banner
```

For the wrong name, point the Deployment's volume at the ConfigMap the team
edits:

```sh
kubectl -n {{ .Namespace }} patch deployment banner -p '{"spec":{"template":{"spec":{"volumes":[{"name":"banner","configMap":{"name":"banner-config-v2"}}]}}}}'
```

Both change the pod template, so the Deployment rolls out new pods. A second
valid path for the wrong name is to write `Winter sale 2026` into
`banner-config`, the ConfigMap the pods mount: the kubelet refreshes the
volume within its sync period, up to about a minute, with no rollout. For
the subPath variant, mounting the whole ConfigMap at
`/usr/share/nginx/html` without `subPath` also works and makes the next edit
reach the pods on its own. The checks read the Service's page, the ready
replica count and what each running pod serves, so every route passes.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/banner
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} exec <pod-name> -- cat /usr/share/nginx/html/index.html
kubectl -n {{ .Namespace }} run curl --rm -it --restart=Never --image=curlimages/curl:8.14.1 -- curl -sS -m 5 http://banner.{{ .Namespace }}.svc/
```

`rollout status` ends with `deployment "banner" successfully rolled out`,
`get pods` shows two pods `1/1` Running with an AGE of seconds, `cat` prints
`Winter sale 2026` in each new pod, and the curl pod prints `Winter sale
2026`. Run the curl twice: the Service balances over both pods, and a single
request that hits a fresh pod hides a stale one.

## In the real world

- Env vars and subPath mounts freeze a ConfigMap at pod start. Teams that
  rely on it either mount the whole ConfigMap and let the kubelet refresh
  it, or trigger a rollout on every edit: Helm's checksum annotation on the
  pod template, Kustomize's `configMapGenerator` with a hashed name, or
  Reloader-style controllers that restart Deployments when a ConfigMap
  changes.
- Rename a ConfigMap and the runbook, the Deployment and the pipeline all
  need the same change. A `-v2` object left next to the old one is a trap
  that `kubectl get configmaps` exposes in one line.
- `kubectl get <object> -o yaml --show-managed-fields` dates every write
  and names its manager (`kubectl-patch`, `kubectl-edit`, a controller), so
  "when did the edit happen" and "did the pods start after it" are two
  commands apart.
- Verify against what users read, not against the object you wrote: a curl
  through the Service, twice, is the check that would have caught this
  before the ticket.

## Go deeper

- ConfigMaps, consumed as env vars and as volumes, and when mounted values
  update: https://kubernetes.io/docs/concepts/configuration/configmap/
- Volumes, the subPath section and its note on updates:
  https://kubernetes.io/docs/concepts/storage/volumes/#using-subpath
- Restarting a Deployment's rollout:
  https://kubernetes.io/docs/reference/kubectl/generated/kubectl_rollout/kubectl_rollout_restart/
