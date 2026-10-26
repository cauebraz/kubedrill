# Addons

The manifests in this directory are copied from upstream releases and keep
their upstream license, Apache-2.0 ([LICENSES/Apache-2.0.txt](../../LICENSES/Apache-2.0.txt)).
The rest of the repository is MIT ([LICENSE](../../LICENSE)).

Each file is byte-identical to its upstream release except the changes
listed below. A modified file starts with a one-line notice naming its source
and its changes.

| File | Upstream source | Version | Local changes |
|------|-----------------|---------|---------------|
| `metrics-server/components.yaml` | https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.9.0/components.yaml | v0.9.0 | `--kubelet-insecure-tls` (kind kubelets serve self-signed certificates), image digest, notice line |
| `kwok/kwok.yaml` | https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/kwok.yaml | v0.8.0 | image digest, notice line |
| `kwok/stage-fast.yaml` | https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/stage-fast.yaml | v0.8.0 | none |
| `gateway/install.yaml` | https://github.com/envoyproxy/gateway/releases/download/v1.9.2/install.yaml (includes the Gateway API v1.6.1 CRDs) | v1.9.2 | `docker.io/` registry prefix and image digests, notice line |

`*/addon.yaml` and `gateway/gatewayclass.yaml` are written for kubedrill and
are MIT. The gateway addon also runs the cloud-provider-kind image
(Apache-2.0) as a container; no file of it is vendored.

None of these upstream projects ships a `NOTICE` file for these releases.

## Adding or bumping an addon

1. Download the release file and diff it against the vendored copy.
2. Change only image pins and the flags the addon needs; note each flag in
   `addon.yaml`.
3. Keep or update the notice line at the top of each modified file.
4. Update the table above.
