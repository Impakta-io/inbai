Status: current — `inbai doctor` reports DNS, reverse DNS, port 25, TLS, and blocklists. `inbaid` writes one JSON line per request. Common relay and startup errors are covered below.

# Troubleshooting

`inbai doctor` is the first check when mail does not flow. It reads the zone file from the server named in the config file and compares it with what this machine can see.

```sh
inbai doctor
```

Each line is `ok` or `fix`. The command exits 0 only when every line is `ok`.

| Line | A fix means |
| --- | --- |
| `DNS` | Publish the quoted MX or TXT record. TXT is where SPF, DKIM, and DMARC live. |
| `PTR` | Ask the host's IP provider to set reverse DNS to the mail hostname. |
| `Port 25` | Allow inbound TCP 25 from the internet to the mail host. |
| `TLS` | Serve a certificate on port 443 that public clients trust and that names the mail host. The Compose stack's generated certificate does not. |
| `Blocklist` | The address is listed, or the list refused the query. Request removal before sending mail. |

`doctor` checks Spamhaus ZEN and SpamCop. It does not send a message.

## Request logs

`inbaid` writes one JSON object per request to standard error. The fields are `time`, `requestId`, `method`, `path`, `status`, and `durationMs`. The path has no query string. Headers and bodies are not written, so a token in `Authorization` does not appear.

The response header `X-Request-Id` is the same value. Send that header when you want to choose the ID. Use letters, digits, `.`, `_`, and `-`, up to 64 characters. Quote `requestId` when you report a failed call.

## Relay authentication failures

`inbai domain relay` authenticates to Stalwart as `operator@<domain>` using `INBAI_OPERATOR_PASSWORD` (or `INBAI_RECOVERY_PASSWORD`), not the service API key. Common failures:

- **Permission denied from Stalwart on `domain relay`:** servers initialized before the operator role existed do not have MTA route permissions on `operator@<domain>`. Start Stalwart in recovery mode and run `inbai init` again to update the operator role, then leave recovery mode and retry `inbai domain relay`.
- **401 from Stalwart on `domain relay`:** `INBAI_OPERATOR_PASSWORD` does not match the password set when `inbai init` ran.
- **Missing relay password:** `inbai domain relay` reads the upstream SMTP password only from `INBAI_RELAY_PASSWORD`. It is not accepted as a flag.
- **Upstream relay rejects mail or TLS fails:** check `--port` (default `587`), `--implicit-tls` for port `465`, and `--allow-invalid-certs` only for a private relay whose certificate is not publicly trusted.

## Signing key mismatch on startup

When `INBAI_DATABASE_URL` is set, `inbaid` stores the access-token signing key in PostgreSQL. If you also set `INBAI_SIGNING_KEY`, it must match the stored key. A different value stops startup with `signing key does not match the database`. Unset `INBAI_SIGNING_KEY` to adopt the stored key, or set the same value on every replica.
