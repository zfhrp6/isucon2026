#!/usr/bin/env bash
set -euo pipefail

readonly DEFAULT_HOST=isu-asia2
readonly LOG_PATH=/var/log/postgresql/postgresql-16-main.log

usage() {
	cat <<'USAGE'
Usage: ops/truncate-postgresql-logs.sh [--dry-run] [--help] [isu-asia2]

Truncate only the current PostgreSQL 16/main log on isu-asia2. Save the log
with ops/fetch-postgresql-logs.sh before truncating it. PostgreSQL remains
running and may append new lines immediately after truncation.

The remote operation checks the fixed log path and then uses truncate in
place. It does not rotate or delete files, and does not modify PostgreSQL
data, configuration, or service state. The removed log history cannot be
restored unless it was saved separately.

--dry-run prints the target without invoking ssh.
USAGE
}

die() {
	printf 'truncate-postgresql-logs.sh: %s\n' "$*" >&2
	exit 2
}

dry_run=false
hosts=()
for arg in "$@"; do
	case "$arg" in
		--help|-h) usage; exit 0 ;;
		--dry-run) dry_run=true ;;
		--*) die "unknown option: $arg" ;;
		*) hosts+=("$arg") ;;
	esac
done
((${#hosts[@]} > 0)) || hosts=("$DEFAULT_HOST")

validated_hosts=()
for host in "${hosts[@]}"; do
	[[ "$host" == "$DEFAULT_HOST" ]] || die "unsupported host: $host"
	for previous in "${validated_hosts[@]}"; do
		[[ "$host" != "$previous" ]] || die "duplicate host: $host"
	done
	validated_hosts+=("$host")
done

if $dry_run; then
	printf 'Would truncate the current PostgreSQL log on:\n'
	for host in "${validated_hosts[@]}"; do
		printf '  %s: %s\n' "$host" "$LOG_PATH"
	done
	exit 0
fi

command -v ssh >/dev/null 2>&1 || die 'ssh is required'
for host in "${validated_hosts[@]}"; do
	printf 'Truncating current PostgreSQL log on %s...\n' "$host"
	if ! ssh -o BatchMode=yes -o ConnectTimeout=10 "$host" 'sudo -n bash -s' <<'REMOTE_TRUNCATE'
set -euo pipefail
path=/var/log/postgresql/postgresql-16-main.log
if [[ -L "$path" ]]; then
	printf 'refusing symlink: %s\n' "$path" >&2
	exit 1
fi
if [[ ! -e "$path" ]]; then
	printf 'missing log file: %s\n' "$path" >&2
	exit 1
fi
if [[ ! -f "$path" ]]; then
	printf 'not a regular file: %s\n' "$path" >&2
	exit 1
fi
if [[ ! -r "$path" || ! -w "$path" ]]; then
	printf 'log file is not readable and writable: %s\n' "$path" >&2
	exit 1
fi
truncate -s 0 --no-create -- "$path"
REMOTE_TRUNCATE
	then
		printf 'Failed on %s; the log may already have been truncated.\n' "$host" >&2
		exit 1
	fi
	printf 'Current PostgreSQL log truncated on %s; new lines may appear immediately.\n' "$host"
done
