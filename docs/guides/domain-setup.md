Status: current — `inbai domain add` prints the zone file and checks MX and TXT. `inbai domain relay` sets an authenticated SMTP relay.

# Domain setup

`inbai domain add` reads the zone file Stalwart computed for the domain. Copy those records into the domain's DNS zone, then run the command again. Each record gets one status. `published` means this machine sees the record. `not published` means it does not see it yet. `mismatch` means a record exists at that name but its value differs. `lookup failed` means the DNS query itself failed, so the record is unknown. The command still exits 0 in that case, because the first run happens before the records exist.

```sh
inbai domain add
```

`--domain` selects a domain other than the one stored by `inbai init`. `inbai domain add` creates that domain in Stalwart when the server does not have it, and prints `Created domain <name> in Stalwart.` (`"created": true` with `--json`). It creates only the domain you named. `--config` selects the config file. `--tls-insecure` skips certificate verification for a host that is not localhost. Localhost skips verification on its own, because the Compose stack uses the certificate Stalwart generates.

The zone file includes MX, SPF, DKIM, and DMARC, plus the client and certificate records Stalwart adds. The check covers MX and TXT records, which is where SPF, DKIM, and DMARC are published. Reverse DNS, port 25, TLS, and blocklists are checked by [`inbai doctor`](troubleshooting.md). `inbai doctor` only reads. For a domain Stalwart does not have, it reports `domain is not configured in Stalwart` with the fix `inbai domain add --domain <name>` and exits 1. Blocklists are queried for IPv4 addresses. An IPv6 address is listed as `not checked` and does not fail the run.

## Ports

inbAI publishes SMTP on port 25 for inbound mail and HTTPS on port 443. Agents send mail through the inbAI API, not SMTP submission, so no submission port is published. Stalwart also listens on port 465 (submission with implicit TLS) inside its container, but the stack does not publish it.

## Outbound mail

With no relay, Stalwart delivers mail for other domains to their MX hosts. `inbai domain relay` replaces that path with one authenticated SMTP server. Mail for a domain this server hosts still stays local.

```sh
export INBAI_OPERATOR_PASSWORD='the-operator-password'
export INBAI_RELAY_PASSWORD='the-relay-password'
inbai domain relay --host smtp.example.net --user mailer --port 587
```

`inbai domain relay --off` restores MX delivery. The command and the passwords are described in [CLI](cli.md). The Compose file does not run a relay. Point this command at a relay you operate.
