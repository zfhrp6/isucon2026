#!/usr/bin/env bash
set -euo pipefail

readonly -a DEFAULT_HOSTS=(isu-asia1 isu-europe1 isu-america1)
readonly -a ALLOWED_HOSTS=(isu-asia1 isu-europe1 isu-america1)

usage() {
	cat <<'EOF'
Usage: ops/truncate-nginx-logs.sh [--dry-run] [--include-error] [--help] [host ...]

Truncate the current access log on the selected application servers. With no
hosts, target isu-asia1, isu-europe1, then isu-america1. By default only
/var/log/nginx/access.ltsv.log is truncated. Add --include-error to also
truncate /var/log/nginx/error.log. The old combined access.log is never used.

Fetch logs with ops/fetch-nginx-logs.sh before truncating them. Nginx may append
new requests immediately after a successful truncate. A failure on a later
host or log cannot restore files already truncated.

Each host's complete selected file set is checked before any file on that host
is truncated. Missing, non-regular, or symlinked files are rejected. The
operation uses sudo -n and truncate --no-create; it does not rotate, reload,
restart, or delete server files.

--dry-run prints the plan without invoking ssh.
EOF
}

die() {
	printf 'truncate-nginx-logs.sh: %s\n' "$*" >&2
	exit 2
}

dry_run=false
include_error=false
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
		--include-error)
			include_error=true
			;;
		--*) die "unknown option: $arg" ;;
		*) hosts+=("$arg") ;;
	esac
done

if ((${#hosts[@]} == 0)); then
	hosts=("${DEFAULT_HOSTS[@]}")
fi

# Validate all hosts before opening an SSH connection.
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

log_names=(access.ltsv.log)
if $include_error; then
	log_names+=(error.log)
fi

if $dry_run; then
	printf 'Would truncate on each selected host:\n'
	for host in "${validated_hosts[@]}"; do
		printf '  %s:' "$host"
		printf ' /var/log/nginx/%s' "${log_names[@]}"
		printf '\n'
	done
	exit 0
fi

command -v ssh >/dev/null 2>&1 || die 'ssh is required'

remote_script=$'set -euo pipefail\npaths=(\n'
for log_name in "${log_names[@]}"; do
	remote_script+="  /var/log/nginx/$log_name"$'\n'
done
remote_script+=$')\n'
remote_script+=$'for path in "${paths[@]}"; do\n'
remote_script+=$'  if [[ -L "$path" ]]; then\n'
remote_script+=$'    printf "refusing symlink: %s\\n" "$path" >&2\n'
remote_script+=$'    exit 1\n'
remote_script+=$'  fi\n'
remote_script+=$'  if [[ ! -e "$path" ]]; then\n'
remote_script+=$'    printf "missing log file: %s\\n" "$path" >&2\n'
remote_script+=$'    exit 1\n'
remote_script+=$'  fi\n'
remote_script+=$'  if [[ ! -f "$path" ]]; then\n'
remote_script+=$'    printf "not a regular file: %s\\n" "$path" >&2\n'
remote_script+=$'    exit 1\n'
remote_script+=$'  fi\n'
remote_script+=$'  if [[ ! -r "$path" || ! -w "$path" ]]; then\n'
remote_script+=$'    printf "log file is not readable and writable: %s\\n" "$path" >&2\n'
remote_script+=$'    exit 1\n'
remote_script+=$'  fi\n'
remote_script+=$'done\n'
remote_script+=$'for path in "${paths[@]}"; do\n'
remote_script+=$'  truncate -s 0 --no-create -- "$path"\n'
remote_script+=$'done\n'

for host in "${validated_hosts[@]}"; do
	printf 'Truncating selected nginx logs on %s...\n' "$host"
	if ! ssh -o BatchMode=yes -o ConnectTimeout=10 "$host" 'sudo -n bash -s' <<< "$remote_script"; then
		printf 'Failed on %s; earlier hosts or files may already be truncated and cannot be restored.\n' "$host" >&2
		exit 1
	fi
	printf 'Selected nginx logs truncated on %s; new lines may appear immediately.\n' "$host"
done
