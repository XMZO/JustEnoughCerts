#!/bin/sh
# Writes the address ranges a CDN connects to the origin from, as settings
# of the reverse proxy in front of JEC: with them, the proxy takes the
# client's address from what the CDN sends, and from the CDN only.
#
#   cdn-ranges.sh cloudflare|cloudfront caddy|nginx FILE
#
# Caddy imports FILE in its global "servers" option, nginx includes it in
# the server block of the panel (docs/operations/reverse-proxy.md). FILE
# is replaced only when the lists download, so a cron job can run this,
# then reload the proxy. It needs curl, and jq for CloudFront.
set -eu

usage() {
	echo "usage: $0 cloudflare|cloudfront caddy|nginx FILE" >&2
	exit 2
}

[ $# -eq 3 ] || usage
cdn=$1
proxy=$2
file=$3
case $proxy in caddy | nginx) ;; *) usage ;; esac

dir=$(mktemp -d)
out=
trap 'rm -rf "$dir" ${out:+"$out"}' EXIT

fetch() {
	curl -fsS --max-time 60 --retry 2 -o "$dir/$1" "$2"
}

case $cdn in
cloudflare)
	fetch v4 https://www.cloudflare.com/ips-v4
	fetch v6 https://www.cloudflare.com/ips-v6
	# The lists end without a newline.
	{
		cat "$dir/v4"
		echo
		cat "$dir/v6"
		echo
	} >"$dir/ranges"
	;;
cloudfront)
	# The ranges CloudFront reaches origins from, not those of its edge.
	fetch json https://ip-ranges.amazonaws.com/ip-ranges.json
	jq -r '(.prefixes[] | select(.service == "CLOUDFRONT_ORIGIN_FACING") | .ip_prefix),
		(.ipv6_prefixes[] | select(.service == "CLOUDFRONT_ORIGIN_FACING") | .ipv6_prefix)' \
		"$dir/json" >"$dir/ranges"
	;;
*) usage ;;
esac

# Nothing but ranges goes into the proxy's settings.
sed -e 's/[[:space:]]//g' -e '/^$/d' "$dir/ranges" >"$dir/clean"
if [ ! -s "$dir/clean" ] || grep -Eqv '^[0-9A-Fa-f.:]+/[0-9]+$' "$dir/clean"; then
	echo "$0: the ranges of $cdn did not download as expected" >&2
	exit 1
fi

# Beside FILE, to replace it in one step.
out=$(mktemp "$file.XXXXXX")
{
	echo "# The address ranges of $cdn, written by cdn-ranges.sh on $(date -u +%Y-%m-%d)."
	case $proxy in
	caddy)
		printf 'trusted_proxies static'
		tr '\n' ' ' <"$dir/clean" | sed 's/ $//; s/^/ /'
		echo
		case $cdn in
		cloudflare) echo "client_ip_headers CF-Connecting-IP" ;;
		# CloudFront appends the viewer to X-Forwarded-For: read it from the right.
		cloudfront) echo "trusted_proxies_strict" ;;
		esac
		;;
	nginx)
		sed 's/.*/set_real_ip_from &;/' "$dir/clean"
		case $cdn in
		cloudflare) echo "real_ip_header CF-Connecting-IP;" ;;
		cloudfront)
			echo "real_ip_header X-Forwarded-For;"
			echo "real_ip_recursive on;"
			;;
		esac
		;;
	esac
} >"$out"

# The proxy may run as another user: the ranges are no secret.
chmod 644 "$out"
mv -f "$out" "$file"
out=
