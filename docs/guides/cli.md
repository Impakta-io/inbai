Status: partial — `init`, `domain add`, `doctor`, `inbox create`, `inbox generate`, `message send`, and `message list` are implemented. The other commands are not.

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

`inbai inbox create <local-part>` creates one address in the domain from the config file. The local part is lowercased. The service account impersonates the new inbox, so the command does not print a mailbox password. The address and its Stalwart account id are written into the config file.

```sh
inbai inbox create alice
```

That creates `alice@example.com` when the config domain is `example.com`.

`inbai inbox generate --count 10` creates `u00000@example.com` through `u00009@example.com`. `--prefix` changes the local-part prefix. The command prints progress after each batch and records every address in the config file. A count above 10000 is rejected. On `inbaid`, a count above 50 is returned as a job instead of waiting in the request.

## message send and message list

`inbai message send` submits one plain-text message as an address created with `inbox create`. `--attach` adds one file. `inbai message list` prints the subject and preview of messages in that inbox.

```sh
inbai message send --from alice@example.com --to bob@example.com --subject hello --body 'hello'
inbai message list alice@example.com
```

Delivery to another server still depends on the DNS records from `domain add` and on Stalwart accepting the message. These commands do not print JSON.

## Planned sections

- Global flags, including JSON output for scripts.
- `login`.
- Domain checks beyond MX and TXT, and the outbound relay.
- `inbox list` and `inbox delete`.
- `agent create`, `agent bind`, `agent suspend`, and `agent revoke`.
