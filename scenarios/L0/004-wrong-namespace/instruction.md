# Catalog copied into the shop namespace, storefront cannot reach it

## Ticket

> **From:** Ravi (catalog team)\
> **Priority:** high
>
> We are consolidating services, so this morning I moved the catalog into
> the shop namespace: copied its Deployment and Service manifests from the
> old `catalog` namespace, adjusted them by hand for `{{ .Namespace }}` and
> applied them. kubectl printed `created` for everything and the pods are
> Running. But the storefront's catalog panel is empty: from the storefront
> pod, `wget -qO- http://catalog/` answers `Connection refused` right away.
> The old namespace served the same page for two years. Can platform check
> what I copied wrong?

## Before you start

A Service has no idea which pods belong to it; it only holds a selector, a
set of labels, and a port mapping. The cluster lists the Ready pods whose
labels match in an EndpointSlice, and every connection to the Service goes to
one of those addresses on the Service's `targetPort`. `kubectl describe
service` prints the selector, the target port and the endpoints side by side.
Compare them with the pods before changing anything.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: the storefront (Deployment `storefront`, Service `storefront`,
  ConfigMap `storefront-html`), the catalog objects Ravi applied this
  morning (a Deployment with 2 replicas, container `web`, Service `catalog`
  on port 80, ConfigMap `catalog-html` with the catalog page) and nothing
  else
- You have cluster-admin on the local kind cluster
- Every other environment runs the catalog as Deployment `catalog`, pods
  labeled `app.kubernetes.io/name=catalog`, nginx `1.27-alpine` listening on
  port 80 and serving the files of `catalog-html`, behind Service `catalog`
  on port 80

## Done when

- service catalog serves the ACME Catalog 3.2.0 page
- service catalog has at least one ready endpoint
- deployment catalog exists
- deployment catalog has 2 ready replicas
- deployment catalog finished rolling out: every pod runs the current template
- no deployment other than catalog and storefront remains in the namespace

## Constraints

- Keep the Service name `catalog` and the ConfigMap `catalog-html`
- Leave the storefront objects as they are
- Do not delete the namespace
