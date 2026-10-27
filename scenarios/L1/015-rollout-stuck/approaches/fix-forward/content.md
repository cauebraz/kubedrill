Fixing forward changes only the field revision 2 broke, so release 2.5.0
stays deployed: the readiness probe path for a 404, the cpu request for
`Insufficient cpu`, the image tag for `Failed to pull image`. Run the one
line that matches the Event, not all three. The change is a new template,
so the Deployment starts a new rollout and the stuck ReplicaSet is replaced.

Choose it when the cause is certain and small, the release carries changes
the team needs, and the old capacity holds while the new pods start. With
`maxUnavailable: 0` the old pods keep serving the whole time; with 25% the
rollout runs one pod short until it completes.

The trade-off is risk under pressure: a second guess that is also wrong
starts a third revision and another wait for the progress deadline. Make
the same change in the repository, or the next deploy brings the broken
field back.
