`kubectl create service nodeport` writes a NodePort Service with the port
mapping and the node port in one command, but its selector is always
`app=<name>`, a label the admin pod does not carry, so the Service starts with
`Endpoints: <none>`. `kubectl set selector` replaces the selector with the
label pairs that identify the stable admin pod, and the EndpointSlice fills
within a second.

Choose it when you want the node port fixed from the first second (no
window on a random port) and no file. It is the route to remember for a
timed exam: both commands are short and `create service nodeport --help`
shows the flags.

The trade-off is the half-built step: between the two commands the Service
exists with no endpoints, and a forgotten second command leaves a Service
that opens 30080 and refuses every connection. Read `describe service admin`
after the second command and confirm one address under Endpoints.
