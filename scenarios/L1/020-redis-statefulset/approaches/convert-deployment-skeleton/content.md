`kubectl create service clusterip --clusterip=None` writes the headless
Service in one line and sets the selector `app=redis`, the label that
`kubectl create deployment redis` also puts on its pod template. The
Deployment skeleton then becomes a StatefulSet: change the kind, add
`serviceName: redis` and the `volumeClaimTemplates` block, add the readiness
probe and the volume mount, and remove `strategy`, which the StatefulSet
schema rejects (`unknown field "spec.strategy"`), and `status`, which the
server owns.

Choose it under time pressure, as in an exam: the generators get the
labels, selector and image right, and only the StatefulSet fields are
typed. The trade-off is that the edit is easy to get half right; a
leftover `strategy` field is rejected on apply, and the labels it
generates (`app=redis`) are whatever the generator picked, not a naming
scheme the team chose.
