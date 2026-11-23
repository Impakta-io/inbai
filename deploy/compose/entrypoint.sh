#!/bin/sh
set -eu

cat > /etc/stalwart/config.json <<EOF
{
  "@type": "PostgreSql",
  "host": "postgres",
  "port": 5432,
  "database": "stalwart",
  "authUsername": "stalwart",
  "authSecret": {"@type": "Value", "secret": "${PG_PASSWORD}"}
}
EOF

exec /usr/local/bin/stalwart --config /etc/stalwart/config.json
