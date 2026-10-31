#!/usr/bin/env bash
set -euo pipefail

readonly -a DEFAULT_HOSTS=(isu-asia1 isu-europe1 isu-america1 isu-asia2)
readonly -a ALLOWED_HOSTS=(isu-asia1 isu-europe1 isu-america1 isu-asia2)

usage() {
	cat <<'EOF'
Usage: ops/reboot.sh [--dry-run] [--help] [host ...]

Schedule an OS reboot on all four contest servers by default. Optional host
arguments select one or more servers from this allowlist:
isu-asia1, isu-europe1, isu-america1, isu-asia2.

Each server accepts `sudo -n shutdown -r +1`, which schedules its reboot about
one minute later. A successful command confirms the request was accepted; it
does not confirm that the server has rebooted and returned to service. If
isu-asia2 is selected with other hosts, it is always processed last because it
hosts the shared PostgreSQL database and rebooting it interrupts all regions.

--dry-run prints the validated plan without invoking ssh.
EOF
}

die() {
	printf 'reboot.sh: %s\n' "$*" >&2
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

# Validate every target before contacting any server.
for host in "${hosts[@]}"; do
	allowed=false
	for candidate in "${ALLOWED_HOSTS[@]}"; do
		if [[ "$host" == "$candidate" ]]; then
			allowed=true
			break
		fi
	done
	$allowed || die "unsupported host: $host"
done

# Keep the shared database last in the requested order.
ordered_hosts=()
database_selected=false
for host in "${hosts[@]}"; do
	if [[ "$host" == isu-asia2 ]]; then
		database_selected=true
	else
		ordered_hosts+=("$host")
	fi
done
if $database_selected; then
	ordered_hosts+=(isu-asia2)
fi

if $dry_run; then
	for host in "${ordered_hosts[@]}"; do
		printf 'Would schedule OS reboot in about one minute on %s\n' "$host"
	done
	exit 0
fi

command -v ssh >/dev/null 2>&1 || die 'ssh is required'

for host in "${ordered_hosts[@]}"; do
	printf 'Scheduling OS reboot on %s...\n' "$host"
	if ! ssh "$host" 'sudo -n shutdown -r +1'; then
		die "reboot request was not confirmed by $host; later hosts were not contacted"
	fi
	printf 'Reboot request accepted by %s; reboot completion is not verified\n' "$host"
done
