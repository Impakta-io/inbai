Status: planned — filled in when the event feed and webhooks exist.

# Events

Agents will be able to wait for mail without a public inbound port. The feed and the webhooks are not implemented.

## Planned sections

- Long-polling `GET /v1/events`. `wait_for_message` already polls an inbox and is not this feed.
- Registering a webhook, verifying its signature, and retry behavior.
- Ordering per inbox, and why delivery is at least once.
- What an agent should do when it sees the same message twice.
