- Record daemon starts, returned stops and checks refused by lock, ownership,
configuration or last-config safety guards in the bounded, redacted incident
journal, including admission refusals on the daemon's last-configuration stop
path. Offline timeline filters expose the new events and retain dry-run
markers without claiming sends or completed checks. Incident schema 15, timeline JSON schema 10 and
executable-adoption state format 7 prevent older readers losing the evidence.
Unreleased; offline fixtures only, with installed/live acceptance still open.
