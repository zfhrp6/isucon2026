#!/usr/bin/env bash
set -euo pipefail
umask 077

readonly -a DEFAULT_HOSTS=(isu-asia1 isu-europe1 isu-america1)
readonly -a ALLOWED_HOSTS=(isu-asia1 isu-europe1 isu-america1)

usage() {
	cat <<'EOF'
Usage: ops/fetch-nginx-logs.sh [--dry-run] [--help] [host ...]

Fetch the current /var/log/nginx/access.ltsv.log and error.log from each
selected application server. With no hosts, fetch from isu-asia1, isu-europe1,
then isu-america1. Rotated logs are not included.

Configure LTSV access logging first with `ops/deploy-config.sh nginx`. If the
new access log is absent, fetching it fails and no complete output is reported.

Logs are saved under logs/nginx/<UTC timestamp>-<unique id>/<host>/ with
private local directory and file permissions. The script reads logs with
sudo -n cat; it does not rotate, reload, or otherwise change server files.
Because nginx may append to or rotate a log while it is read, each fetched
file is not guaranteed to be an atomic snapshot.

--dry-run prints the plan without invoking ssh or creating directories.
EOF
}

die() {
	printf 'fetch-nginx-logs.sh: %s\n' "$*" >&2
	exit 2
}

dry_run=false
hosts=()
for arg in "$@"; do
	case "$arg" in
		--help|-h)
			usage
			exit 0
			;;
		--dry-run)
			dry_run=true
			;;
		--*) die "unknown option: $arg" ;;
		*) hosts+=("$arg") ;;
	esac
done

if ((${#hosts[@]} == 0)); then
	hosts=("${DEFAULT_HOSTS[@]}")
fi

# Reject every invalid or duplicate target before creating output or connecting.
validated_hosts=()
for host in "${hosts[@]}"; do
	allowed=false
	for candidate in "${ALLOWED_HOSTS[@]}"; do
		if [[ "$host" == "$candidate" ]]; then
			allowed=true
			break
		fi
	done
	$allowed || die "unsupported host: $host"

	for previous in "${validated_hosts[@]}"; do
		[[ "$host" != "$previous" ]] || die "duplicate host: $host"
	done
	validated_hosts+=("$host")
done

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/.." && pwd)
nginx_logs_dir="$repo_root/logs/nginx"

if $dry_run; then
	printf 'Would fetch current access.ltsv.log and error.log into logs/nginx/<UTC timestamp>-<unique id>/:\n'
	for host in "${validated_hosts[@]}"; do
		printf '  %s\n' "$host"
	done
	exit 0
fi

command -v ssh >/dev/null 2>&1 || die 'ssh is required'
command -v date >/dev/null 2>&1 || die 'date is required'
command -v mktemp >/dev/null 2>&1 || die 'mktemp is required'
command -v mkdir >/dev/null 2>&1 || die 'mkdir is required'
command -v chmod >/dev/null 2>&1 || die 'chmod is required'
command -v mv >/dev/null 2>&1 || die 'mv is required'
command -v rm >/dev/null 2>&1 || die 'rm is required'

mkdir -p -- "$nginx_logs_dir"
chmod 0700 -- "$nginx_logs_dir"
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
result_dir=$(mktemp -d "$nginx_logs_dir/${timestamp}-XXXXXX")
chmod 0700 -- "$result_dir"
printf 'Saving fetched logs under %s\n' "$result_dir"

partials=()
cleanup_partials() {
	for partial in "${partials[@]}"; do
		if [[ -n "$partial" ]]; then
			rm -f -- "$partial"
		fi
	done
}
trap cleanup_partials EXIT

for host in "${validated_hosts[@]}"; do
	host_dir="$result_dir/$host"
	mkdir -m 0700 -- "$host_dir"
	for log_name in access.ltsv.log error.log; do
		partial="$host_dir/$log_name.part"
		partials+=("$partial")
		printf 'Fetching %s:%s...\n' "$host" "$log_name"
		if ! ssh -o BatchMode=yes -o ConnectTimeout=10 "$host" "sudo -n cat /var/log/nginx/$log_name" > "$partial"; then
			rm -f -- "$partial"
			printf 'Failed to fetch %s:%s; completed logs are retained under %s\n' "$host" "$log_name" "$result_dir" >&2
			exit 1
		fi
		chmod 0600 -- "$partial"
		mv -- "$partial" "$host_dir/$log_name"
	done
done

printf 'Fetched both current nginx logs from all selected hosts under %s\n' "$result_dir"
