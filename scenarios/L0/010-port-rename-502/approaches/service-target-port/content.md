Point the Service at the port the pod carries. `targetPort` takes a number
or the name of a `containerPort`; the first command prints the pod's ports
so the value in the patch is the name the release uses, and the
EndpointSlice shows the resolved number in PORTS within a second, with no
rollout and no pod restart.

Choose it when the Service is the object left behind: the EndpointSlice has
Ready addresses and either no PORTS or the old number. It is the smallest
change and the fastest to revert. Prefer the name over the number when the
containerPort has one: the Service then follows the pod template on the
next port move instead of breaking again.

It changes nothing when the readiness probe is the field left behind. The
Service already resolves the right port, the EndpointSlice has no ready
endpoint, and the fix belongs in the pod template of the Deployment
(`fix-pod-template`).
