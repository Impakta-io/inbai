Status: partial — `init`, `domain add`, `doctor`, inbox create and generate, message send, list, get, reply, and delete, `mcp serve`, `backup`, and `restore` are implemented. The other commands are not.

# CLI

With no arguments, `inbai` prints `inbai 0.0.0-dev` and exits. `inbai version` prints the same line.

## init

`inbai init` bootstraps Stalwart while it is in recovery mode.

```sh
export INBAI_RECOVERY_PASSWORD='the-recovery-password'
inbai init --url http://127.0.0.1:8080 --domain example.com
```

`--hostname` defaults to `mail.example.com`. `--stalwart-url` is the HTTPS URL stored for later commands and defaults to `https://127.0.0.1`. `--config` sets the file path. The command prints the 30-day retention default.

Running init again reuses the domain, the roles, and the accounts. It keeps the API key already in the config file.

## domain add

`inbai domain add` prints the DNS zone file for the domain in the config file and checks the MX and TXT records. The details are in [domain setup](domain-setup.md).

## doctor

`inbai doctor` checks the same MX and TXT records, then checks reverse DNS, port 25, TLS on port 443, and the Spamhaus ZEN and SpamCop blocklists. A healthy report exits 0. Any check that needs a fix exits 1. How to read a line is in [troubleshooting](troubleshooting.md).

## inbox create

`inbai inbox create <local-part>` asks `inbaid` to create one address. Set `INBAI_OPERATOR_TOKEN` to the same value `inbaid serve` uses. The command uses `apiUrl` from the config file, or `http://127.0.0.1:8787`. The address and account id are written into the config file. `--json` prints the API response instead of a sentence.

```sh
inbai inbox create alice
```

That creates `alice@example.com` when the config domain is `example.com`.

`inbai inbox generate --count 10` asks `inbaid` to create `u00000@example.com` through `u00009@example.com`. `--prefix` changes the local-part prefix. When the server returns a job, the command polls it and prints progress. It records every address in the config file.

## message send and message list

`inbai message send` submits one plain-text message as an address created with `inbox create`. `--attach` adds one file. `inbai message list` prints the subject and preview. `--query` searches message text. `inbai message get` prints one message, `inbai message reply` answers it, and `inbai message delete` removes it. `--json` prints the API document.

```sh
inbai message send --from alice@example.com --to bob@example.com --subject hello --body 'hello'
inbai message list alice@example.com
inbai message get alice@example.com <message-id>
```

Delivery to another server still depends on the DNS records from `domain add` and on Stalwart accepting the message. `inbaid serve` must be running.

## mcp serve

`inbai mcp serve` speaks MCP on stdin and stdout. It exchanges `INBAI_CLIENT_ID` and `INBAI_CLIENT_SECRET` for an access token, or uses `INBAI_ACCESS_TOKEN` or `INBAI_OPERATOR_TOKEN` when one of those is already set. The tools and the HTTP endpoint are described in [MCP](../agents/mcp.md).

## backup and restore

`inbai backup --file path` writes the inbAI database. `inbai restore --file path` replaces that database with the file. Both require `INBAI_DATABASE_URL`. The file has no message bodies. The procedure for the Stalwart copy is in [backup](backup.md).

## Planned sections

- JSON output for `init`, `domain`, and `doctor`.
- `login`.
- Domain checks beyond MX and TXT, and the outbound relay.
- `inbox list` and `inbox delete`.
- `agent create`, `agent bind`, `agent suspend`, and `agent revoke`.
