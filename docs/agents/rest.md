Status: planned — filled in when the REST API exists.

# REST API

The contract will live in [api/openapi.yaml](../../api/openapi.yaml). That file is a skeleton with no paths. The server does not listen yet.

## Planned sections

- The base URL and the `/v1` prefix.
- How to exchange a client credential for an access token.
- Cursor pagination.
- The `Idempotency-Key` header on create and send.
- The error document shape.
- Sending, listing, reading, replying, and deleting, including attachments and threading headers.
