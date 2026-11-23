Status: current

# CLI

With no arguments, `inbai` prints `inbai 0.0.0-dev` and exits. `inbai version` prints the same line.

## init

`inbai init` bootstraps Stalwart while it is in recovery mode.

```sh
export INBAI_RECOVERY_PASSWORD='the-recovery-password'
inbai init --url http://127.0.0.1:8080 --domain example.com
```

`--hostname` defaults to `mail.example.com`. `--stalwart-url` is the HTTPS URL stored for later commands and defaults to `https://127.0.0.1`. `--config` sets the file path. `--json` prints the domain, the operator login, the retention default, and the config path. It does not print the service API key. The command prints the 30-day retention default.

Running init again reuses the domain, the roles, and the accounts. It keeps the API key already in the config file.

## login

`inbai login` verifies credentials against `inbaid` and saves them in the config file (mode `0600`) so later commands do not require exporting tokens on every run.

With an operator token:

```sh
export INBAI_OPERATOR_TOKEN='the-operator-token'
inbai login --url http://127.0.0.1:8787
```

With an OAuth client credential:

```sh
export INBAI_CLIENT_SECRET='the-client-secret'
inbai login --url http://127.0.0.1:8787 --client-id <client-id>
```

`--url` updates `apiUrl` in the config file (defaulting to the saved `apiUrl` or `http://127.0.0.1:8787`). `INBAI_CLIENT_ID` may be used instead of `--client-id`. `--json` prints the API URL, auth mode, client id (when set), and config path; it does not print secrets or bearer tokens. `inbai login --clear` removes stored login credentials from the config file. Environment variables (`INBAI_OPERATOR_TOKEN`, `INBAI_CLIENT_ID`/`INBAI_CLIENT_SECRET`) still take precedence over stored credentials when set.

## domain add

`inbai domain add` prints the DNS zone file for the domain in the config file and checks the MX and TXT records. `--json` prints the zone and each check. The details are in [domain setup](domain-setup.md).

## domain relay

`inbai domain relay` sends mail for other domains through an authenticated SMTP relay. Mail for a domain this server hosts stays local. The password is `INBAI_RELAY_PASSWORD`. The operator password is `INBAI_OPERATOR_PASSWORD`, which is the same value `inbai init` used as `INBAI_RECOVERY_PASSWORD`. The command signs in as `operator@<domain>`. It does not use the service API key.

```sh
export INBAI_OPERATOR_PASSWORD='the-operator-password'
export INBAI_RELAY_PASSWORD='the-relay-password'
inbai domain relay --host smtp.example.net --user mailer
```

`--port` defaults to 587. `--implicit-tls` is for a relay that uses TLS from the first byte. `--allow-invalid-certs` accepts a certificate that does not validate. `--json` prints the host, port, and user. It does not print the password. `inbai domain relay --off` sends other domains to their MX hosts again.

Run `inbai init` again, with Stalwart in recovery mode, before the first relay change on a server that was created before this command. That run gives the operator the route permissions. The service account does not receive them.

## doctor

`inbai doctor` checks the same MX and TXT records, then checks reverse DNS, port 25, TLS on port 443, and the Spamhaus ZEN and SpamCop blocklists. `--json` prints each check and whether the report is healthy. A healthy report exits 0. Any check that needs a fix exits 1. How to read a line is in [troubleshooting](troubleshooting.md).

## inbox create

`inbai inbox create <local-part>` asks `inbaid` to create one address. Set `INBAI_OPERATOR_TOKEN` to the same value `inbaid serve` uses. The command uses `apiUrl` from the config file, or `http://127.0.0.1:8787`. The address and account id are written into the config file. `--json` prints the API response instead of a sentence.

```sh
inbai inbox create alice
```

That creates `alice@example.com` when the config domain is `example.com`.

`inbai inbox list` prints each address and account id. `--limit` sets the page size and `--cursor` asks for the next page. A line that starts with `next` is the cursor to pass back. `--json` prints the API response. `inbai inbox delete alice@example.com` removes that account and the address from the config file. Both commands use the operator token.

`inbai inbox generate --count 10` asks `inbaid` to create `u00000@example.com` through `u00009@example.com`. `--prefix` changes the local-part prefix. When the server returns a job, the command polls it and prints progress. It records every address in the config file.

`inbai inbox allowlist set <inbox-id> ada@example.com example.com` stores a sender allowlist. An entry is one address or one domain. The list holds at most 100 entries. `inbai inbox allowlist get <inbox-id>` prints it. `inbai inbox allowlist clear <inbox-id>` removes it. An operator can do this, and so can an agent with `manage` on that inbox. The list does not stop mail from arriving. A later message read reports whether the sender matched.

`inbai inbox quota get <inbox-id>` prints the disk storage quota and current usage in bytes. `inbai inbox quota set <inbox-id> <limit-bytes>` sets the storage cap (`0` clears the cap). New inboxes receive the server's product default of 100 MiB unless `--quota-bytes` is passed to `inbai inbox create` or `INBAI_STORAGE_QUOTA_BYTES` is changed on `inbaid`. An operator or an agent with `manage` on that inbox may change the quota.

## message send and message list

`inbai message send` submits one plain-text message as an address created with `inbox create`. `--attach` adds one file. `inbai message list` prints the subject and preview. `--query` searches message text. `--limit` and `--cursor` page the result. `inbai message get` prints one message and each attachment as `blobId` and name. `inbai message attachment --output note.txt alice@example.com <message-id> <blob-id>` writes that file. `inbai message reply` answers a message, and `inbai message delete` removes it. `--json` prints the API document.

```sh
inbai message send --from alice@example.com --to bob@example.com --subject hello --body 'hello'
inbai message list alice@example.com
inbai message get alice@example.com <message-id>
```

Delivery to another server still depends on the DNS records from `domain add` and on Stalwart accepting the message. `inbaid serve` must be running.

## agent

`inbai agent create ada` asks `inbaid` for a client id and a secret. The secret is shown once. `inbai agent grant ada <inbox-id> read` records one permission. The permissions on an inbox are `read`, `send`, `reply`, `delete`, and `manage`. `inbai agent grant ada '*' inbox:provision` lets that agent create inboxes. The inbox id for that permission is `*`. A read grant does not include it. `inbai agent revoke ada <inbox-id> read` removes one permission. `inbai agent suspend ada` stops that agent from exchanging a secret or using an access token. `--json` prints the result. All four commands use the operator token.

## audit

`inbai audit` prints actions that created an agent, granted or revoked a permission, suspended an agent, or created or deleted an inbox. `--after` asks for rows after an id. `--limit` sets the page size. A line that starts with `next` is the id to pass back. `--json` prints the API response. The command uses the operator token. A secret is not written in the log.

## retention

`inbai retention` prints how many days the server keeps mail. `--days 14` changes that and writes it into the config file. `--days 0` keeps mail. `--inbox <id> --days 7` sets one inbox and leaves the server period alone. `inbai retention run` deletes mail older than the period now. `inbaid` also does that about once an hour. A message with no received time is kept. `--json` prints the API response. The commands use the operator token.

## mcp serve

`inbai mcp serve` speaks MCP on stdin and stdout. It exchanges `INBAI_CLIENT_ID` and `INBAI_CLIENT_SECRET` for an access token, or uses `INBAI_ACCESS_TOKEN` or `INBAI_OPERATOR_TOKEN` when one of those is already set. The tools and the HTTP endpoint are described in [MCP](../agents/mcp.md).

## backup and restore

`inbai backup --file path` writes the inbAI database. `inbai restore --file path` replaces that database with the file. Both require `INBAI_DATABASE_URL`. `--json` prints the file path and the event and suppression counts. The file has no message bodies. The procedure for the Stalwart copy is in [backup](backup.md).
