#!/bin/sh
set -eu

# Stalwart reads this JSON file. Escape the password for a JSON string with
# shell parameter expansion only, because the image may not provide jq.
# Backslash is escaped first so the backslashes added for quotes survive.
json_replace() {
	rest=$1
	out=
	while :; do
		case $rest in
		*"$2"*)
			out=$out${rest%%"$2"*}$3
			rest=${rest#*"$2"}
			;;
		*) break ;;
		esac
	done
	printf '%s' "$out$rest"
}

json_escape() {
	# A literal newline, carriage return or tab cannot appear in a JSON string.
	nl='
'
	cr=$(printf '\r')
	tab=$(printf '\t')
	case $1 in
	*"$nl"* | *"$cr"* | *"$tab"*)
		echo "PG_PASSWORD must not contain a newline, carriage return or tab" >&2
		exit 1
		;;
	esac
	step=$(json_replace "$1" '\' '\\')
	json_replace "$step" '"' '\"'
}

secret=$(json_escape "${PG_PASSWORD:?set PG_PASSWORD}")

cat > /etc/stalwart/config.json <<EOT
{
  "@type": "PostgreSql",
  "host": "postgres",
  "port": 5432,
  "database": "stalwart",
  "authUsername": "stalwart",
  "authSecret": {"@type": "Value", "secret": "${secret}"}
}
EOT

exec /usr/local/bin/stalwart --config /etc/stalwart/config.json
