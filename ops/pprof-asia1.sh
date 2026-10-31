#!/usr/bin/env bash
set -euo pipefail

readonly HOST=isu-asia1
readonly REMOTE_APP=/home/isucon/webapp/go/isureal
readonly STATE_DIR=/home/isucon/.isureal-pprof-diagnostic
readonly SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly REPO_ROOT=$(cd -- "$SCRIPT_DIR/.." && pwd)

usage() {
	cat <<'EOF'
Usage:
  ops/pprof-asia1.sh enable
  ops/pprof-asia1.sh status
  ops/pprof-asia1.sh capture [windows]
  ops/pprof-asia1.sh restore

enable installs a temporary Go binary built with diagnostic_pprof on isu-asia1.
capture stores consecutive 30-second CPU profiles (1–20 windows) under logs/pprof.
restore puts back the saved binary, but refuses if another deploy changed it.
EOF
}

die() {
	printf 'pprof-asia1.sh: %s\n' "$*" >&2
	exit 2
}

action=${1:-}
[[ -n "$action" ]] || { usage >&2; exit 2; }
shift || true

case "$action" in
	enable)
		(($# == 0)) || die 'enable takes no arguments'
		command -v rsync >/dev/null 2>&1 || die 'rsync is required'
		stage=$(ssh "$HOST" 'mktemp -d /tmp/isureal-pprof.XXXXXX')
		[[ "$stage" =~ ^/tmp/isureal-pprof\.[A-Za-z0-9]+$ ]] || die 'unexpected remote staging path'
		if ! rsync -az --exclude=.git --exclude=/isureal --exclude=/isureal.next "$REPO_ROOT/webapp/go/" "$HOST:$stage/"; then
			ssh "$HOST" "rm -rf -- '$stage'" || true
			die 'source transfer failed'
		fi
		ssh "$HOST" bash -s -- "$stage" "$REMOTE_APP" "$STATE_DIR" <<'REMOTE'
set -Eeuo pipefail
stage=$1
app=$2
state=$3
backup="$state/original"
diagnostic="$state/diagnostic"
changed=false
created_state=false
exec 9>/home/isucon/.isureal-deploy.lock
flock -x 9
cleanup() {
	status=$?
	trap - EXIT
	if [[ $status -ne 0 && "$created_state" == true ]]; then
		if [[ "$changed" == true && -f "$backup" ]]; then
			cp -p -- "$backup" "$app.rollback.$$"
			mv -f -- "$app.rollback.$$" "$app"
			if sudo -n systemctl restart isureal-go && systemctl is-active --quiet isureal-go && [[ $(sha256sum "$app" | awk '{print $1}') == $(cat "$state/original.sha256") ]]; then
				rm -rf -- "$state"
			else
				printf 'automatic rollback needs inspection; saved original remains at %s\n' "$backup" >&2
			fi
		else
			rm -rf -- "$state"
		fi
	fi
	rm -rf -- "$stage"
	exit "$status"
}
trap cleanup EXIT
umask 077
[[ ! -e "$state" ]] || { printf 'diagnostic state already exists; inspect and restore it first\n' >&2; exit 1; }
[[ -f "$app" && ! -L "$app" ]] || { printf 'application binary is not a regular file\n' >&2; exit 1; }
[[ $(sha256sum "$app" | awk '{print $1}') == 77aa3c8f12cc7842eb7df809da32adcfa4e62218fdb111560e500cfb2f14a3c5 ]] || { printf 'original binary differs from preflight; refusing install\n' >&2; exit 1; }
mkdir -- "$state"
created_state=true
cp -p -- "$app" "$backup"
sha256sum "$backup" | awk '{print $1}' > "$state/original.sha256"
cd "$stage"
/home/isucon/local/go/bin/go build -tags diagnostic_pprof -o isureal cmd/isureal/main.go
install -m 0755 -- isureal "$diagnostic"
sha256sum "$diagnostic" | awk '{print $1}' > "$state/diagnostic.sha256"
cp -p -- "$diagnostic" "$app.next"
mv -f -- "$app.next" "$app"
changed=true
sudo -n systemctl restart isureal-go
systemctl is-active --quiet isureal-go
ready=false
for attempt in {1..20}; do
	if curl --fail --silent --max-time 2 http://127.0.0.1:6060/debug/pprof/ >/dev/null 2>&1; then
		ready=true
		break
	fi
	sleep 0.5
done
[[ "$ready" == true ]] || { printf 'pprof listener did not become ready\n' >&2; exit 1; }
printf 'Temporary pprof active on 127.0.0.1:6060; original binary backup: %s\n' "$backup"
changed=false
REMOTE
		;;
	status)
		(($# == 0)) || die 'status takes no arguments'
		ssh "$HOST" bash -s -- "$REMOTE_APP" "$STATE_DIR" <<'REMOTE'
set -Eeuo pipefail
app=$1
state=$2
if [[ -f "$state/original" && -f "$state/diagnostic.sha256" ]]; then
	expected=$(cat "$state/diagnostic.sha256")
	actual=$(sha256sum "$app" | awk '{print $1}')
	if [[ "$expected" == "$actual" ]]; then
		curl --fail --silent --show-error --max-time 3 http://127.0.0.1:6060/debug/pprof/ >/dev/null
		printf 'diagnostic binary active; pprof reachable on loopback\n'
	elif [[ -f "$state/original.sha256" && $(cat "$state/original.sha256") == "$actual" ]]; then
		systemctl is-active --quiet isureal-go
		if curl --silent --max-time 2 http://127.0.0.1:6060/debug/pprof/ >/dev/null 2>&1; then
			printf 'original binary installed but pprof port remains reachable\n' >&2
			exit 1
		fi
		printf 'original binary restored; pprof is closed; saved state retained\n'
	else
		printf 'diagnostic state exists but running binary differs; restore will refuse\n' >&2
		exit 1
	fi
else
	printf 'no saved diagnostic deployment state\n'
fi
REMOTE
		;;
	capture)
		windows=${1:-20}
		(($# <= 1)) || die 'capture takes at most one argument'
		[[ "$windows" =~ ^([1-9]|1[0-9]|20)$ ]] || die 'windows must be 1–20'
		run_id=$(date -u +%Y%m%dT%H%M%SZ)
		output_root="$REPO_ROOT/logs/pprof"
		mkdir -p -- "$output_root"
		output_dir=$(mktemp -d "$output_root/$run_id.XXXXXX")
		binary_tmp="$output_dir/isureal-diagnostic.tmp.$$"
		if ! ssh "$HOST" "cat '$STATE_DIR/diagnostic'" > "$binary_tmp"; then
			rm -f -- "$binary_tmp"
			die 'could not retrieve diagnostic binary'
		fi
		mv -- "$binary_tmp" "$output_dir/isureal-diagnostic"
		for ((window = 1; window <= windows; window++)); do
			printf 'Capturing CPU profile %d/%d (30s)\n' "$window" "$windows"
			profile_tmp="$output_dir/cpu-$window.pprof.tmp.$$"
			if ! ssh "$HOST" 'curl --fail --silent --show-error --max-time 40 "http://127.0.0.1:6060/debug/pprof/profile?seconds=30"' > "$profile_tmp"; then
				rm -f -- "$profile_tmp"
				die "CPU profile $window failed"
			fi
			mv -- "$profile_tmp" "$output_dir/cpu-$window.pprof"
		done
		printf 'Profiles saved under %s\n' "$output_dir"
		;;
	restore)
		(($# == 0)) || die 'restore takes no arguments'
		ssh "$HOST" bash -s -- "$REMOTE_APP" "$STATE_DIR" <<'REMOTE'
set -Eeuo pipefail
app=$1
state=$2
backup="$state/original"
exec 9>/home/isucon/.isureal-deploy.lock
flock -x 9
[[ -f "$backup" && -f "$state/original.sha256" && -f "$state/diagnostic.sha256" ]] || { printf 'saved original binary or checksum missing; refusing restore\n' >&2; exit 1; }
expected_diagnostic=$(cat "$state/diagnostic.sha256")
actual=$(sha256sum "$app" | awk '{print $1}')
expected_original=$(cat "$state/original.sha256")
backup_hash=$(sha256sum "$backup" | awk '{print $1}')
[[ "$expected_original" == "$backup_hash" ]] || { printf 'saved original binary checksum mismatch\n' >&2; exit 1; }
if [[ "$expected_diagnostic" == "$actual" ]]; then
	cp -p -- "$backup" "$app.restore.$$"
	mv -f -- "$app.restore.$$" "$app"
elif [[ "$expected_original" != "$actual" ]]; then
	printf 'application binary changed after pprof install; refusing to overwrite it\n' >&2
	exit 1
fi
sudo -n systemctl restart isureal-go
systemctl is-active --quiet isureal-go
restored=$(sha256sum "$app" | awk '{print $1}')
[[ "$expected_original" == "$restored" ]] || { printf 'restored binary checksum mismatch\n' >&2; exit 1; }
if curl --silent --max-time 2 http://127.0.0.1:6060/debug/pprof/ >/dev/null 2>&1; then
	printf 'pprof still reachable after restore; inspect service before retrying\n' >&2
	exit 1
fi
printf 'original binary restored and verified; saved state retained at %s\n' "$state"
REMOTE
		;;
	-h|--help|help)
		usage
		;;
	*) die "unknown action: $action" ;;
esac
