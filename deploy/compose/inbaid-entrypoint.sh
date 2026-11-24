#!/bin/sh
set -eu

# Build the database URL here so the password is percent-encoded. Every byte
# outside the RFC 3986 unreserved set becomes %XX. The C locale makes the
# shell walk the password one byte at a time.
urlencode() {
	LC_ALL=C
	rest=$1
	out=
	while [ -n "$rest" ]; do
		tail=${rest#?}
		c=${rest%"$tail"}
		case $c in
		[A-Za-z0-9._~-]) out=$out$c ;;
		*) out=$out$(printf '%%%02X' $(($(printf '%d' "'$c") & 255))) ;;
		esac
		rest=$tail
	done
	printf '%s' "$out"
}

INBAI_DATABASE_URL="postgres://stalwart:$(urlencode "${PG_PASSWORD:?set PG_PASSWORD}")@postgres:5432/inbai?sslmode=disable"
export INBAI_DATABASE_URL
unset PG_PASSWORD

config=/config/config.json
echo "waiting for ${config}"
while [ ! -f "$config" ]; do
	sleep 2
done

exec /usr/local/bin/inbaid serve --config "$config" --addr 0.0.0.0:8787
