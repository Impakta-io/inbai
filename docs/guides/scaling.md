Status: partial — how to run the load tool, and how replicas share the database, are described here. No reference-host measurement is published.

# Scaling

The supported deployment is one Linux host running `inbaid`, PostgreSQL, and Stalwart. This page says how to measure that host and how a second `inbaid` process shares the database. It does not publish a measured result. Run the tool on the host you care about and keep the JSON it prints.

## Measure one run

`inbaid serve` must already be running. The tool provisions addresses, sends one extra probe message, then lists and reads.

```sh
export INBAI_OPERATOR_TOKEN='the-operator-token'
go run ./tools/load -url http://127.0.0.1:8787 -inboxes 10 -messages 1 -concurrency 4
```

`-inboxes` and `-messages` are the size of that run. A count above 50 addresses becomes a job, and the tool waits for it. The domain send quota still applies, so a large send run stops with HTTP 429 until the operator raises the quota. The JSON report counts those as errors.

`events` is `seen` when the probe message appears on `GET /v1/events`, `empty` when the wait ends without one, and `unavailable` when the server has no database. `eventLagMs` is the time from the probe send returning to seeing that event. A server without `INBAI_DATABASE_URL` cannot record one.

The numbers in the JSON are that run only. This repository does not treat them as a capacity claim.

## More than one inbaid

Each replica uses the same `INBAI_DATABASE_URL` and the same `INBAI_SIGNING_KEY`. The event log, cursors, webhook deliveries, and the suppression list are in that database, so a replica that restarts can read them.

Credentials, grants, send quotas, idempotency keys, and bulk jobs are still kept in the process that created them. A second replica does not see those. Putting a load balancer in front of several replicas does not share them.

Stalwart stays a single node in this deployment. inbAI does not use a Stalwart Enterprise feature. A second Stalwart node is a later layout, and this project does not ship one.

PostgreSQL can move off the host by changing `INBAI_DATABASE_URL`. Stalwart's messages stay in Stalwart's own database and volume. Copy both before that move, as [backup](backup.md) describes.
