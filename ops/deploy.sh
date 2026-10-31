#!/usr/bin/env bash
set -euo pipefail

readonly -a DEFAULT_HOSTS=(isu-asia1 isu-europe1 isu-america1)
readonly -a ALLOWED_HOSTS=(isu-asia1 isu-europe1 isu-america1)
readonly REMOTE_APP_DIR=/home/isucon/webapp/go
readonly REMOTE_GO=/home/isucon/local/go/bin/go

usage() {
	cat <<'EOF'
Usage: ops/deploy.sh [--dry-run] [--help] [host ...]

Deploy webapp/go source to the three application servers by default. Optional
host arguments select one or more application servers from this allowlist:
isu-asia1, isu-europe1, isu-america1.

The script stages the current local source in /tmp on each server, builds it
there, atomically replaces the application binary, then restarts isureal-go.
It does not transfer local .env files or env.sh; the service keeps using its
server-side /home/isucon/env.sh configuration. The final check confirms only
that the systemd unit is active; it does not test HTTP behavior.
It never uses rsync --delete. Removed source files can therefore remain in the
server's application directory, but they are not included in the staged build.
Deploy SQL or static assets separately when those change.

--dry-run prints the validated deployment plan without invoking ssh or rsync.
EOF
}

die() {
	printf 'deploy.sh: %s\n' "$*" >&2
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

# Validate every target before doing any remote work.
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

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/.." && pwd)
source_dir="$repo_root/webapp/go"
[[ -d "$source_dir" ]] || die "source directory not found: $source_dir"
[[ -f "$source_dir/go.mod" ]] || die "go.mod not found in $source_dir"
[[ -f "$source_dir/cmd/isureal/main.go" ]] || die "Go entry point not found"

if $dry_run; then
	printf 'Source: %s\n' "$source_dir"
	for host in "${hosts[@]}"; do
		printf 'Would stage, build, atomically install, and restart isureal-go on %s\n' "$host"
	done
	exit 0
fi

command -v rsync >/dev/null 2>&1 || die 'rsync is required'
command -v ssh >/dev/null 2>&1 || die 'ssh is required'

for host in "${hosts[@]}"; do
	printf 'Deploying to %s...\n' "$host"
	stage=$(ssh "$host" 'mktemp -d /tmp/isureal-deploy.XXXXXX')
	[[ "$stage" =~ ^/tmp/isureal-deploy\.[A-Za-z0-9]+$ ]] || die "unexpected staging path from $host: $stage"
	if ! rsync -az \
		--exclude='._*' \
		--exclude='AGENTS.md' \
		--exclude='.env' \
		--exclude='.env.*' \
		--exclude='env.sh' \
		--exclude='/isureal' \
		--exclude='/isureal.next' \
		"$source_dir/" "$host:$stage/"; then
		# The validated mktemp path is intentionally embedded in the remote cleanup command.
		# shellcheck disable=SC2029
		ssh "$host" "rm -rf -- '$stage'" || true
		die "rsync failed for $host"
	fi
	# The validated mktemp path is intentionally embedded in the remote command.
	# shellcheck disable=SC2029
	if ! ssh "$host" "set -eu; trap 'rm -rf -- $stage' EXIT; cd '$stage'; '$REMOTE_GO' build -o isureal cmd/isureal/main.go; install -m 0755 isureal '$REMOTE_APP_DIR/isureal.next'; mv -f '$REMOTE_APP_DIR/isureal.next' '$REMOTE_APP_DIR/isureal'; sudo -n systemctl restart isureal-go; systemctl is-active --quiet isureal-go"; then
		die "build or service restart failed for $host"
	fi
	printf 'Binary installed; service restart and active-state check passed on %s (HTTP unverified)\n' "$host"
done
