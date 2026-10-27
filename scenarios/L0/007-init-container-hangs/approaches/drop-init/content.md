Removing the `initContainers` list lets the app container start at once:
the checks grade ready replicas, endpoints and the response through the
Service, so the pods turn Ready and the grade passes. It is a mitigation,
not a fix. The migration step exists because the app expects the schema the
DBA team flagged; skipping it ships an api that may answer 200 to a health
request and fail on the first real query, and it hides the mismatch that
made the loop wait, so the next environment gets the same ticket.

Choose it only when the team confirms the migrations already ran against
this database and the loop is purely a guard, or as a time-boxed step to
restore service while the real value is corrected. Record it in the
incident notes and put the init container back with the corrected value as
soon as the outage is over.
