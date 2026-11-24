Status: current

# Deploy

The single-host stack is in [compose](compose/README.md). It starts PostgreSQL, Stalwart, and `inbaid`. Without a clone of this repository, `inbai init` writes and starts its own stack of PostgreSQL and Stalwart instead, as in the [quickstart](../docs/guides/quickstart.md). `inbai init` performs the first boot, `inbai domain add` prints the DNS records, and `inbai doctor` checks the host.
