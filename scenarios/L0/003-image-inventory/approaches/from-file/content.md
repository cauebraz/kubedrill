Write the list to a file first, then store the file under the key `images`
with `--from-file=images=images.txt`. The file is the step you can look at
before anything is written to the cluster: `cat images.txt` shows the lines
the ConfigMap will hold, and a wrong line (an init image, an image of a
Deployment at 0 replicas) is one `grep -v` away from gone.

Choose it when the list is more than a few lines or when someone reviews it
before it lands, which is how an audit artifact is usually produced. The
trade-off is the key name: `--from-file=images.txt` without the `images=`
prefix names the key after the file, and the audit job that reads
`data.images` finds nothing. Always write the key explicitly.
