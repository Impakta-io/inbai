Status: partial — how to run the load tool, and how replicas share the database, are described here. No reference-host measurement is published.

# Scaling

The supported deployment is one Linux host running `inbaid`, PostgreSQL, and Stalwart. This page says how to measure that host and how a second `inbaid` process shares the database. It does not publish a measured result. Run the tool on the host you care about and keep the JSON it prints.

## Measure one run

`inbaid serve` must already be running. The tool provisions addresses, sends one extra probe message, then lists and reads.

```sh
# At least 32 characters. Generate one with openssl, or use the token `inbaid` runs with.
export INBAI_OPERATOR_TOKEN="$(openssl rand -hex 32)"
go run ./tools/load -url http://127.0.0.1:8787 -inboxes 10 -messages 1 -concurrency 4
```

`-inboxes` and `-messages` are the size of that run. A count above 50 addresses becomes a job, and the tool waits for it. The send quotas and per-recipient-domain rate limit still apply, so a large send run stops with HTTP 429 until the operator raises them via `INBAI_QUOTA_INBOX_DAILY`, `INBAI_QUOTA_DOMAIN_DAILY`, `INBAI_QUOTA_WORKSPACE_DAILY`, and `INBAI_RATE_RECIPIENT_PER_MINUTE`. The JSON report counts those as errors.

`events` is `seen` when the probe message appears on `GET /v1/events`, `empty` when the wait ends without one, and `unavailable` when the server has no database. `eventLagMs` is the time from the probe send returning to seeing that event. A server without `INBAI_DATABASE_URL` cannot record one.

The numbers in the JSON are that run only. This repository does not treat them as a capacity claim.

## More than one inbaid

Each replica uses the same `INBAI_DATABASE_URL`. That database holds the signing key, so the replicas verify the same access tokens. Set `INBAI_SIGNING_KEY` only when every process must use a key you chose, and set the same value on each. The event log, cursors, webhook deliveries, the job queue, the suppression list, grants, client credentials, send quotas, idempotency keys, and bulk jobs are in that database, so a replica that restarts can read them. Any replica can claim bulk generation, cursor sweeps, retention sweeps, and webhook deliveries using `FOR UPDATE SKIP LOCKED`.

Stalwart stays a single node in this deployment. inbAI does not use a Stalwart Enterprise feature. A second Stalwart node is a later layout, and this project does not ship one.

PostgreSQL can move off the host by changing `INBAI_DATABASE_URL`. Stalwart's messages stay in Stalwart's own database and volume. Copy both before that move, as [backup](backup.md) describes.
