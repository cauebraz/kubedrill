The JSON patch replaces the `volumeMounts` entry so the volume is mounted as
the whole directory `/usr/share/nginx/html` instead of one file with
`subPath: index.html`. The template changed, so the Deployment rolls out new
pods, and from now on the kubelet refreshes the directory within its sync
period after every edit: the next banner change needs no restart.

Choose it in the subPath variant when the directory holds nothing else the
container needs, which is the case here: the ConfigMap's `items` places the
key as `index.html`, the only file nginx serves. It turns a one-off fix into
a permanent one.

The trade-off is that a directory mount hides everything the image shipped
at that path (nginx's default `index.html` and `50x.html`), so a container
that needs other files there keeps `subPath` and relies on restarts instead.
In the env var variant there is no volume to patch, and in the wrong-name
variant the mount is already a directory, so read the pod template first.
