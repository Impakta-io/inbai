Status: current — `inbai doctor` reports DNS, reverse DNS, port 25, TLS, and blocklists. `inbaid` writes one JSON line per request. Common relay, init, and startup errors are covered below.

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
| `TLS` | Serve a certificate on port 443 that public clients trust and that names the mail host. The certificate Stalwart generates does not, and inbAI does not install one. Configure it in Stalwart. A local trial keeps this line as a fix. |
| `Blocklist` | The address is listed, or the list refused the query. Request removal before sending mail. |

`doctor` checks Spamhaus ZEN and SpamCop. It does not send a message.

## Request logs

`inbaid` writes one JSON object per request to standard error. The fields are `time`, `requestId`, `method`, `path`, `status`, and `durationMs`. The path has no query string. Headers and bodies are not written, so a token in `Authorization` does not appear.

The response header `X-Request-Id` is the same value. Send that header when you want to choose the ID. Use letters, digits, `.`, `_`, and `-`, up to 64 characters. Quote `requestId` when you report a failed call.

## Relay authentication failures

`inbai domain relay` authenticates to Stalwart as `operator@<domain>`, not with the service API key. It uses `INBAI_OPERATOR_PASSWORD` when set, then the operator password Docker-mode `inbai init` stored in `config.json`, then `INBAI_RECOVERY_PASSWORD`. Common failures:

- **Permission denied from Stalwart on `domain relay`:** the operator role on `operator@<domain>` lacks the MTA route permissions. Run `inbai init` again. A rerun adds any permission a role is missing. Then retry `inbai domain relay`. Docker mode handles recovery mode itself. An external Stalwart must be started in recovery mode first and left afterwards.
- **`domain relay needs INBAI_OPERATOR_PASSWORD` after Docker-mode init:** the config was written by an older `inbai`, which did not keep the operator password. Run `inbai init` again with the same domain. It sets a new operator password and stores it.
- **401 from Stalwart on `domain relay`:** the password does not match `operator@<domain>`. Unset `INBAI_OPERATOR_PASSWORD` to use the stored one, or, after external-mode init, export the value init used as `INBAI_RECOVERY_PASSWORD`.
- **Missing relay password:** `inbai domain relay` reads the upstream SMTP password only from `INBAI_RELAY_PASSWORD`. It is not accepted as a flag.
- **Upstream relay rejects mail or TLS fails:** check `--port` (default `587`), `--implicit-tls` for port `465`, and `--allow-invalid-certs` only for a private relay whose certificate is not publicly trusted.

## Init times out

Docker-mode `inbai init` pulls images first and prints Docker's progress. Stalwart then has 5 minutes to answer. When it does not, init prints the Stalwart container's state (for example `stalwart exited with code 2`), then the last 40 lines of the Stalwart log, then the last 15 lines of the PostgreSQL log, and stops. It ends with a hint chosen from those logs:

- **`Permission denied` in the Stalwart log:** a mount is not writable by the container user. Fix the owner or mode of the mounted directory, then run `inbai init` again.
- **`password authentication failed` in the PostgreSQL log:** see the next paragraph.
- **Stalwart exited with neither:** read the Stalwart log above the hint.
- **Anything else:** Stalwart did not answer in time; read both logs.

A common cause of the password failure is a volume left by an earlier install with the same Compose project. Its PostgreSQL database keeps the password it was created with, so Stalwart cannot sign in with the new one. Init prints the exact command to remove those volumes only in this case. It has this form:

```sh
docker compose -p inbai -f "<config-dir>/stack/compose.yaml" down -v
```

That deletes the earlier install's mail, inboxes, and accounts. Run `inbai init` again afterwards.

## `inbai` gets 401 from `inbaid`

An exported `INBAI_OPERATOR_TOKEN` comes before `operatorToken` in `config.json`, for `inbai` and for `inbaid serve`. When the two processes see different values, every call is `401`. Unset the variable in both shells to use the stored token, or export the same value in both.

## Requests fail with 401 after `docker compose down -v`

The service API key in `config.json` belonged to the deleted Stalwart database. Run `inbai init` again with the same domain. It sees that Stalwart refuses the key, issues a new one, and saves it. Then restart `inbaid`.

## Secret too short on startup

`inbaid serve` exits with code 2 when `INBAI_SIGNING_KEY` is shorter than 32 bytes or the operator token (`INBAI_OPERATOR_TOKEN`, or `operatorToken` in the config file) is shorter than 32 characters. A short signing key lets any agent holding an access token search for the key offline and then mint operator tokens. A short operator token can be guessed over the network. Generate a value that passes with `openssl rand -hex 32`. `inbai init` and `inbai login` refuse a shorter `INBAI_OPERATOR_TOKEN` for the same reason. Values that `inbai init` and `inbaid` generate are long enough.

When the database already holds a signing key shorter than 32 bytes, because an older version stored a short `INBAI_SIGNING_KEY`, `inbaid` refuses to start and says so. Stop every `inbaid` process, then run `DELETE FROM signing_key;` in the `inbai` database and start again. `inbaid` stores a new random key, or the `INBAI_SIGNING_KEY` you set if it is at least 32 bytes. Access tokens issued before that stop working, and each agent gets a new one by exchanging its client secret.

## Signing key mismatch on startup

When `inbaid` has a database (`databaseUrl` in `config.json`, or `INBAI_DATABASE_URL`), it stores the access-token signing key in PostgreSQL. If you also set `INBAI_SIGNING_KEY`, it must match the stored key. A different value stops startup with `signing key does not match the database`. Unset `INBAI_SIGNING_KEY` to adopt the stored key, or set the same value on every replica.
