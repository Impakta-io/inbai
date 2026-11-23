Status: partial — `inbai domain add` prints the zone file and checks MX and TXT records. Reverse DNS and the outbound relay are not implemented.

# Domain setup

`inbai domain add` reads the zone file Stalwart computed for the domain. Copy those records into the domain's DNS zone, then run the command again. A record that is visible from this machine is marked `published`. A record that is not visible yet is marked `not published`. The command still exits 0 in that case, because the first run happens before the records exist.

```sh
inbai domain add
```

`--domain` selects a domain other than the one stored by `inbai init`. A domain that does not exist yet is created. `--config` selects the config file. `--tls-insecure` skips certificate verification for a host that is not localhost. Localhost skips verification on its own, because the Compose stack uses the certificate Stalwart generates.

The zone file includes MX, SPF, DKIM, and DMARC, plus the client and certificate records Stalwart adds. The check covers MX and TXT records, which is where SPF, DKIM, and DMARC are published.

## Planned sections

- Reverse DNS for the sending host.
- Direct delivery and the optional authenticated SMTP relay.
- What `inbai doctor` checks, and how to read a failure.
