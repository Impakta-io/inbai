Status: partial — install, domain setup, inbox creation, and a local send are documented. Public delivery still depends on DNS and a healthy `doctor` report.

# Quickstart

This walks an operator from the Compose stack to one inbox. An agent connects with a client credential, described in the [agent overview](../agents/overview.md). MCP is described in [MCP](../agents/mcp.md).

## Install

Install the CLI with Homebrew (or download a release archive / build from source — see [install](install.md)):

```sh
brew install impakta-io/tap/inbai
```

Then start the Compose stack in [deploy/compose](../../deploy/compose/README.md). Leave Stalwart in recovery mode for the next step.

## Bootstrap

```sh
export INBAI_RECOVERY_PASSWORD='the-recovery-password'
inbai init --url http://127.0.0.1:8080 --domain example.com
```

Set `STALWART_RECOVERY_MODE=0`, start the stack again, then print the DNS zone:

```sh
inbai domain add
```

Publish the zone, then run `inbai doctor`. A healthy host exits 0. The Compose certificate is self-signed, so the TLS line stays a fix until a real certificate is installed.

## Create an inbox

The Compose service listens on `127.0.0.1:8787` after init writes `config.json` and Stalwart leaves recovery mode. The token is the one passed to Compose as `INBAI_OPERATOR_TOKEN`. You can save it in `config.json` once with `inbai login`, or keep exporting `INBAI_OPERATOR_TOKEN`:

```sh
export INBAI_OPERATOR_TOKEN='choose-a-token'
inbai login --url http://127.0.0.1:8787
inbai inbox create alice
inbai message send --from alice@example.com --to alice@example.com --subject hello --body hello
inbai message list alice@example.com
```

`message list` prints the subject and a preview. Mail from the public internet arrives only after the DNS records are published and Stalwart accepts the delivery.
