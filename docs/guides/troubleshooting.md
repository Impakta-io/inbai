Status: partial — `inbai doctor` reports DNS, reverse DNS, port 25, TLS, and blocklists. `inbaid` writes one JSON line per request. Relay authentication failures are not a separate section.

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

## Planned sections

- Relay authentication failures.
