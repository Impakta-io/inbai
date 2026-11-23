Status: partial — the event feed, signed webhooks, and the ingest hint exist when `INBAI_DATABASE_URL` is set. Suppression from hard bounces is not implemented.

# Events

Agents can wait for mail without a public inbound port. The durable record is a JMAP state cursor per inbox in PostgreSQL. A Stalwart `message-ingest` webhook only tells `inbaid` to ask for changes since that cursor. Stalwart drops a hint it cannot deliver, and the cursor is still there after `inbaid` restarts.

Set `INBAI_DATABASE_URL` before `inbaid serve`. The server creates the tables. Without that URL, `GET /v1/events` and `POST /v1/webhooks` answer 503.

## Feed

`GET /v1/events?inbox=ACCOUNT_ID&after=0&timeout=25` returns events with `id` greater than `after`, oldest first. `timeout` is seconds and is capped at 25. The response is `{"events":[{"id":1,"inbox":"ACCOUNT_ID","messageId":"...","kind":"created"}]}`. `kind` is `created` or `destroyed`.

Keep the largest `id` you have handled and pass it as `after` on the next call. Delivery is at least once and ordered per inbox, so the same `id` can be seen again after a timeout. Handle it once, then move the cursor forward.

A caller needs a `read` grant on that inbox. Another inbox is forbidden and the log is not read.

`wait_for_message` uses this feed when the database is configured. The tool result's subject field is the event kind, and the id is the message id to pass to `get_message`.

## Webhooks

```sh
curl -s -X POST -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"inbox":"ACCOUNT_ID","url":"https://agent.example/hook"}' \
  http://127.0.0.1:8787/v1/webhooks
```

The response includes `secret` once. Each delivery is a POST of one event. The header `X-Inbai-Signature` is `sha256=` plus the hex HMAC-SHA256 of the raw body, using that secret. A non-2xx response is retried with backoff. After five attempts the delivery stops and the event remains on the feed.

## Ingest hint

Stalwart posts to `POST /v1/hooks/ingest` with `X-Inbai-Ingest` set to `INBAI_INGEST_SECRET`. The body is Stalwart's event list. `message-ingest` events are matched on `data.to`. The numeric `accountId` is not the JMAP account id. A slow sweep also asks for changes when no hint arrives.

## Operations

`GET /healthz` answers when the process is up. `GET /readyz` pings PostgreSQL when the event database is configured. `GET /metrics` exposes `inbai_events_appended_total` and `inbai_webhooks_delivered_total`.

## Planned sections

- A suppression list fed by hard bounces.
- What an agent should do when a message body conflicts with an earlier instruction. The event itself is only an id and a kind.
