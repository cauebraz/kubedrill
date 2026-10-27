Write the Service as a manifest and apply it. Every field the ticket cares
about is explicit in one place: the type, the selector with both label pairs,
the `port`, the `targetPort` read from the Deployment and the `nodePort`
30080. The snippet uses YAML flow style so it fits a few lines; the block
style of the walkthrough is the one to commit to a repository.

Choose it when the Service is going to live in the team's repository, which
is every GitOps setup: the file is the source of truth, the review shows
exactly which pods the selector matches, and the next pipeline run recreates
the same Service. It is also the only route that sets `nodePort` in one
step.

The trade-off is that nothing checks the selector against the pods for you.
`kubectl expose` copies the Deployment's selector; a hand-written manifest
copies whatever you typed, so read `describe service admin` and the
EndpointSlice after the apply, every time.
