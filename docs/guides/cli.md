Status: partial — `init` is implemented. The other commands are not.

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

## Planned sections

- Global flags, including JSON output for scripts.
- `login` and `doctor`.
- `domain add` and domain checks.
- `inbox create`, `inbox generate`, `inbox list`, and `inbox delete`.
- Sending, listing, reading, replying, and deleting mail.
- `agent create`, `agent bind`, `agent suspend`, and `agent revoke`.
