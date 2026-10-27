Write the Service and the StatefulSet in one file, check it with a
server-side dry run, apply it and wait for the rollout. The dry run asks the
API server to validate and default the objects without storing them, so a
typo in `volumeClaimTemplates` or a selector that does not match the
template labels fails before anything is created. That matters more for a
StatefulSet than for a Deployment: `serviceName`, `selector`,
`podManagementPolicy` and `volumeClaimTemplates` cannot change after the
create, and a mistake there means recreating the StatefulSet.

Choose it whenever the result must be reproducible, which is every database:
the file goes into the team's repository and the next cluster gets the same
three nodes. The trade-off is typing: a StatefulSet has no `kubectl create`
generator, so the YAML is written by hand or from the documentation's
example.
