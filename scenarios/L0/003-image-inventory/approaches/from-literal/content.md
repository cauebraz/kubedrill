One shell line: the flat jsonpath prints every main-container image
separated by spaces, `tr` turns the spaces into line breaks, `sort -u`
orders and deduplicates, and `--from-literal=images=...` stores the result
under the key `images`. A multi-line value in `--from-literal` is fine; the
double quotes around `$(...)` keep the line breaks.

Choose it when the reporter asked for a one-liner they can paste for other
namespaces: swap the namespace and it runs again. The trade-off is that
nothing is reviewed before it is written; if the list is wrong, the
ConfigMap must be deleted and created again, since `create` refuses to
overwrite an existing object. The flat form also needs the `tr` step,
because `{.items[*].spec.containers[*].image}` joins its results with
spaces, not line breaks.
