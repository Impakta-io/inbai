#!/bin/sh
set -eu

config=/config/config.json
echo "waiting for ${config}"
while [ ! -f "$config" ]; do
	sleep 2
done

exec /usr/local/bin/inbaid serve --config "$config" --addr 0.0.0.0:8787
