#!/usr/bin/env bash
set -euo pipefail

readonly HOST=isu-asia1
readonly REMOTE_APP=/home/isucon/webapp/go/isureal
readonly SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

usage() {
	cat <<'EOF'
Usage:
  ops/pprof-asia1.sh --source-root <checkout> start [windows]
  ops/pprof-asia1.sh start [windows]
  ops/pprof-asia1.sh stop
  ops/pprof-asia1.sh capture-current [windows]
  ops/pprof-asia1.sh enable <measurement-id> <expected-original-sha256>
  ops/pprof-asia1.sh status <measurement-id>
  ops/pprof-asia1.sh capture <measurement-id> [windows]
  ops/pprof-asia1.sh restore <measurement-id>

enable installs a temporary Go binary built with diagnostic_pprof on isu-asia1.
capture stores consecutive 30-second CPU profiles (1–20 windows) under logs/pprof.
restore puts back the saved binary, but refuses if another deploy changed it.
Use a new measurement ID for each deployment; saved backups are retained.
start enables pprof and begins background capture; stop ends capture and restores the binary.
Both manage one session under the main worktree's logs/pprof. The explicit-ID commands remain available for recovery.
--source-root selects the checkout to build; by default the script's checkout is used.
EOF
}

die() {
	printf 'pprof-asia1.sh: %s\n' "$*" >&2
	exit 2
}

source_root="$SCRIPT_DIR/.."
if [[ ${1:-} == --source-root ]]; then
	(($# >= 3)) || die '--source-root requires a checkout and a command'
	source_root=$2
	shift 2
fi
source_root=$(cd -- "$source_root" && pwd -P) || die 'source checkout is not accessible'
readonly REPO_ROOT="$source_root"
SCRIPT_REPO_ROOT=$(git -C "$SCRIPT_DIR/.." rev-parse --show-toplevel) || die 'cannot inspect script repository'
readonly SCRIPT_REPO_ROOT
script_common_dir=$(git -C "$SCRIPT_REPO_ROOT" rev-parse --path-format=absolute --git-common-dir) || die 'cannot inspect script repository metadata'
source_common_dir=$(git -C "$REPO_ROOT" rev-parse --path-format=absolute --git-common-dir) || die 'source checkout is not in a Git repository'
script_common_dir=$(cd -- "$script_common_dir" && pwd -P) || die 'script repository metadata is inaccessible'
source_common_dir=$(cd -- "$source_common_dir" && pwd -P) || die 'source repository metadata is inaccessible'
[[ "$source_common_dir" == "$script_common_dir" ]] || die '--source-root must be a checkout of the script repository'
source_top=$(git -C "$REPO_ROOT" rev-parse --show-toplevel) || die 'cannot determine source checkout root'
[[ "$source_top" == "$REPO_ROOT" ]] || die '--source-root must name a checkout root, not a subdirectory'
main_repo_root=$(git -C "$SCRIPT_REPO_ROOT" worktree list --porcelain | sed -n '1{s/^worktree //;p;}') || die 'cannot inspect script repository worktrees'
main_repo_root=$(cd -- "$main_repo_root" && pwd -P) || die 'main Git worktree is inaccessible'
readonly MAIN_REPO_ROOT="$main_repo_root"
[[ -n "$MAIN_REPO_ROOT" && -d "$MAIN_REPO_ROOT" ]] || die 'could not determine the main Git worktree; refusing a local fallback'
local_state_dir="$MAIN_REPO_ROOT/logs/pprof"
active_file="$local_state_dir/active-session"
active_lock="$local_state_dir/.active-session.lock"

read_active() {
	[[ -f "$active_file" && ! -L "$active_file" ]] || die 'no active local pprof session'
	mapfile -t active_lines < "$active_file"
	((${#active_lines[@]} == 2)) || die 'active session record is malformed; inspect it manually'
	active_id=${active_lines[0]}
	active_hash=${active_lines[1]}
	[[ "$active_id" =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$ && "$active_hash" =~ ^[0-9a-f]{64}$ ]] || die 'active session record is invalid; inspect it manually'
}

write_active() {
	local tmp
	tmp=$(mktemp "$local_state_dir/.active-session.XXXXXX")
	chmod 600 "$tmp"
	printf '%s\n%s\n' "$1" "$2" > "$tmp"
	mv -f -- "$tmp" "$active_file"
}

run_low_level() {
	local subcommand=$1
	shift
	bash "$SCRIPT_DIR/$(basename -- "$0")" --source-root "$REPO_ROOT" "$subcommand" "$@"
}

action=${1:-}
[[ -n "$action" ]] || { usage >&2; exit 2; }
shift || true

case "$action" in
	start)
		windows=${1:-20}
		(($# <= 1)) || die 'start takes at most one windows argument'
		[[ "$windows" =~ ^([1-9]|1[0-9]|20)$ ]] || die 'windows must be 1–20'
		mkdir -p -- "$local_state_dir"
		exec 9>"$active_lock"
		flock -x 9
		[[ ! -e "$active_file" && ! -L "$active_file" ]] || die 'an active local session already exists; stop or inspect it first'
		measurement_id="$(date -u +%Y%m%dT%H%M%SZ)-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
		session_dir="$local_state_dir/session-$measurement_id"
		mkdir -m 700 -- "$session_dir"
		original_hash=$(ssh "$HOST" "sha256sum '$REMOTE_APP' | cut -d ' ' -f1")
		[[ "$original_hash" =~ ^[0-9a-f]{64}$ ]] || die 'could not read a valid remote binary SHA-256'
		source_head=$(git -C "$REPO_ROOT" rev-parse HEAD) || die 'could not record source checkout HEAD'
		[[ "$source_head" =~ ^[0-9a-f]{40,64}$ ]] || die 'source checkout returned an invalid HEAD'
		# Persist before enabling: if the SSH result is ambiguous, stop still knows which remote state to inspect.
		write_active "$measurement_id" "$original_hash"
		if run_low_level enable "$measurement_id" "$original_hash"; then
			:
		else
			printf 'start did not complete; session %s remains recorded for safe recovery\n' "$measurement_id" >&2
			exit 1
		fi
		diagnostic_hash=$(ssh "$HOST" "cat '/home/isucon/.isureal-pprof-diagnostic.$measurement_id/diagnostic.sha256'") || die 'could not read installed diagnostic binary checksum'
		[[ "$diagnostic_hash" =~ ^[0-9a-f]{64}$ ]] || die 'installed diagnostic binary checksum is invalid'
		printf '%s\n' "$REPO_ROOT" > "$session_dir/source-root.txt"
		printf '%s\n' "$source_head" > "$session_dir/source-head.txt"
		printf '%s\n' "$original_hash" > "$session_dir/original.sha256"
		printf '%s\n' "$diagnostic_hash" > "$session_dir/diagnostic.sha256"
		touch -- "$session_dir/collector-requested"
		nohup env PPROF_CONTROL_DIR="$session_dir" bash "$SCRIPT_DIR/$(basename -- "$0")" --source-root "$REPO_ROOT" capture "$measurement_id" "$windows" > "$session_dir/collector.log" 2>&1 < /dev/null 9>&- &
		collector_pid=$!
		printf '%s\n' "$collector_pid" > "$session_dir/collector.pid"
		ready=false
		for attempt in {1..60}; do
			if [[ -s "$session_dir/collector-ready" ]]; then
				ready=true
				break
			fi
			if [[ -s "$session_dir/collector-done" ]]; then
				break
			fi
			sleep 1
		done
		if [[ "$ready" != true ]]; then
			printf 'collector did not become ready; recovery record retained; details: %s\n' "$session_dir/collector.log" >&2
			exit 1
		fi
		profile_dir=$(<"$session_dir/collector-ready")
		[[ -d "$profile_dir" && "$profile_dir" == "$local_state_dir"/* ]] || die 'collector reported an invalid profile directory; recovery record retained'
		printf '%s\n' "$profile_dir" > "$session_dir/profiles.path"
		printf 'pprof session %s started; profiles: %s\n' "$measurement_id" "$profile_dir"
		exit 0
		;;
	stop)
		(($# == 0)) || die 'stop takes no arguments'
		[[ -d "$local_state_dir" ]] || die 'no active local pprof session'
		exec 9>"$active_lock"
		flock -x 9
		read_active
		session_dir="$local_state_dir/session-$active_id"
		collector_status=0
		if [[ -e "$session_dir/collector-requested" ]]; then
			touch -- "$session_dir/collector-stop"
			collector_done=false
			for attempt in {1..55}; do
				if [[ -s "$session_dir/collector-done" ]]; then
					collector_done=true
					break
				fi
				sleep 1
			done
			if [[ "$collector_done" == true ]]; then
				read -r collector_status < "$session_dir/collector-done"
				[[ "$collector_status" =~ ^[0-9]+$ ]] || collector_status=1
			else
				collector_status=1
				collector_timeout=true
				printf 'collector did not finish within 55 seconds; restoring service and retaining partial data\n' >&2
			fi
		fi
		if run_low_level restore "$active_id"; then
			if [[ ${collector_timeout:-false} != true ]]; then
				rm -f -- "$active_file"
			fi
			if [[ ${collector_timeout:-false} == true ]]; then
				printf 'service restored, but collector may still be finishing; recovery pointer retained. Retry stop after it exits.\n' >&2
				exit 1
			fi
			if [[ "$collector_status" == 0 ]]; then
				printf 'pprof session %s stopped; run data and remote backup retained\n' "$active_id"
			else
				printf 'pprof session %s restored, but collection failed or timed out; details: %s\n' "$active_id" "$session_dir/collector.log" >&2
				exit 1
			fi
		else
			# Restore can be unnecessary only when enable never changed anything. Confirm disk,
			# running process, and listener state before removing our recovery pointer.
			if ssh "$HOST" bash -s -- "$REMOTE_APP" "$active_id" "$active_hash" <<'REMOTE_CHECK'
set -Eeuo pipefail
app=$1
id=$2
baseline=$3
state="/home/isucon/.isureal-pprof-diagnostic.$id"
exec 9>/home/isucon/.isureal-deploy.lock
flock -x 9
[[ ! -e "$state" ]] || exit 1
[[ $(sha256sum "$app" | awk '{print $1}') == "$baseline" ]]
systemctl is-active --quiet isureal-go
pid=$(systemctl show --property=MainPID --value isureal-go)
[[ "$pid" =~ ^[1-9][0-9]*$ ]]
[[ $(sha256sum "/proc/$pid/exe" | awk '{print $1}') == "$baseline" ]]
[[ -z $(ss -H -ltn 'sport = :6060') ]]
[[ $(curl --silent --max-time 2 --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8080/api/me) == 401 ]]
REMOTE_CHECK
			then
				rm -f -- "$active_file"
				printf 'pprof session %s never changed the running binary; recovery pointer cleared\n' "$active_id"
			else
				printf 'restore failed and safe no-change state was not proven; recovery record retained\n' >&2
				exit 1
			fi
		fi
		exit 0
		;;
	capture-current)
		windows=${1:-20}
		(($# <= 1)) || die 'capture-current takes at most one windows argument'
		[[ "$windows" =~ ^([1-9]|1[0-9]|20)$ ]] || die 'windows must be 1–20'
		[[ -d "$local_state_dir" ]] || die 'no active local pprof session'
		exec 9>"$active_lock"
		flock -s 9
		read_active
		measurement_id=$active_id
		session_dir="$local_state_dir/session-$measurement_id"
		[[ ! -e "$session_dir/collector-requested" ]] || die 'automatic collector owns this session; use stop to finish and restore it'
		flock -u 9
		run_low_level capture "$measurement_id" "$windows"
		exit $?
		;;
esac

case "$action" in
	enable|status|capture|restore)
		measurement_id=${1:-}
		[[ "$measurement_id" =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$ ]] || die 'measurement ID must be 1–64 letters, digits, underscores or hyphens'
		shift
		readonly STATE_DIR="/home/isucon/.isureal-pprof-diagnostic.$measurement_id"
		;;
esac

case "$action" in
	enable)
		(($# == 1)) || die 'enable requires the expected original binary SHA-256'
		expected_original=$1
		[[ "$expected_original" =~ ^[0-9a-f]{64}$ ]] || die 'expected original SHA-256 must contain 64 lowercase hex digits'
		command -v rsync >/dev/null 2>&1 || die 'rsync is required'
		stage=$(ssh "$HOST" 'mktemp -d /tmp/isureal-pprof.XXXXXX')
		[[ "$stage" =~ ^/tmp/isureal-pprof\.[A-Za-z0-9]+$ ]] || die 'unexpected remote staging path'
		if ! rsync -az --exclude=.git --exclude=/isureal --exclude=/isureal.next "$REPO_ROOT/webapp/go/" "$HOST:$stage/"; then
			ssh "$HOST" "rm -rf -- '$stage'" || true
			die 'source transfer failed'
		fi
		ssh "$HOST" bash -s -- "$stage" "$REMOTE_APP" "$STATE_DIR" "$expected_original" <<'REMOTE'
set -Eeuo pipefail
stage=$1
app=$2
state=$3
expected_original=$4
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
			rollback_ready=false
			if sudo -n systemctl restart isureal-go && [[ $(sha256sum "$app" | awk '{print $1}') == $(cat "$state/original.sha256") ]]; then
				for attempt in {1..20}; do
					if systemctl is-active --quiet isureal-go; then
						pid=$(systemctl show --property=MainPID --value isureal-go)
					else
						pid=
					fi
					if [[ "$pid" =~ ^[1-9][0-9]*$ ]] \
						&& [[ $(sha256sum "/proc/$pid/exe" | awk '{print $1}') == "$(cat "$state/original.sha256")" ]] \
						&& [[ -z $(ss -H -ltn 'sport = :6060') ]] \
						&& [[ $(curl --silent --max-time 2 --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8080/api/me) == 401 ]]; then
						rollback_ready=true
						break
					fi
					sleep 0.5
				done
			fi
			if [[ "$rollback_ready" == true ]]; then
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
[[ $(sha256sum "$app" | awk '{print $1}') == "$expected_original" ]] || { printf 'original binary differs from preflight; refusing install\n' >&2; exit 1; }
[[ -z $(ss -H -ltn 'sport = :6060') ]] || { printf 'pprof port is already in use; refusing install\n' >&2; exit 1; }
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
	if systemctl is-active --quiet isureal-go; then
		pid=$(systemctl show --property=MainPID --value isureal-go)
	else
		pid=
	fi
	if [[ "$pid" =~ ^[1-9][0-9]*$ ]] \
		&& [[ $(sha256sum "/proc/$pid/exe" | awk '{print $1}') == "$(cat "$state/diagnostic.sha256")" ]] \
		&& curl --fail --silent --max-time 2 http://127.0.0.1:6060/debug/pprof/ >/dev/null 2>&1 \
		&& [[ $(curl --silent --max-time 2 --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8080/api/me) == 401 ]]; then
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
		(($# == 0)) || die 'status takes only the measurement ID'
		ssh "$HOST" bash -s -- "$REMOTE_APP" "$STATE_DIR" <<'REMOTE'
set -Eeuo pipefail
app=$1
state=$2
if [[ -f "$state/original" && -f "$state/diagnostic.sha256" ]]; then
	expected=$(cat "$state/diagnostic.sha256")
	actual=$(sha256sum "$app" | awk '{print $1}')
	if [[ "$expected" == "$actual" ]]; then
		systemctl is-active --quiet isureal-go
		pid=$(systemctl show --property=MainPID --value isureal-go)
		[[ "$pid" =~ ^[1-9][0-9]*$ ]]
		[[ $(sha256sum "/proc/$pid/exe" | awk '{print $1}') == "$expected" ]]
		curl --fail --silent --show-error --max-time 3 http://127.0.0.1:6060/debug/pprof/ >/dev/null
		[[ $(curl --silent --max-time 2 --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8080/api/me) == 401 ]]
		printf 'diagnostic binary active; pprof reachable on loopback\n'
	elif [[ -f "$state/original.sha256" && $(cat "$state/original.sha256") == "$actual" ]]; then
		systemctl is-active --quiet isureal-go
		pid=$(systemctl show --property=MainPID --value isureal-go)
		[[ "$pid" =~ ^[1-9][0-9]*$ ]]
		[[ $(sha256sum "/proc/$pid/exe" | awk '{print $1}') == "$actual" ]]
		[[ -z $(ss -H -ltn 'sport = :6060') ]]
		[[ $(curl --silent --max-time 2 --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8080/api/me) == 401 ]]
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
		control_dir=${PPROF_CONTROL_DIR:-}
		if [[ -n "$control_dir" ]]; then
			[[ -d "$control_dir" && ! -L "$control_dir" ]] || die 'collector control directory is invalid'
			capture_finish() {
				capture_status=$?
				trap - EXIT
				control_tmp="$control_dir/.collector-done.$$"
				printf '%s\n' "$capture_status" > "$control_tmp"
				mv -f -- "$control_tmp" "$control_dir/collector-done"
				exit "$capture_status"
			}
			trap capture_finish EXIT
		fi
		run_id=$(date -u +%Y%m%dT%H%M%SZ)
		output_root="$MAIN_REPO_ROOT/logs/pprof"
		mkdir -p -- "$output_root"
		output_dir=$(mktemp -d "$output_root/$run_id.$measurement_id.XXXXXX")
		printf 'Profiles directory: %s\n' "$output_dir"
		printf '%s\n' "$measurement_id" > "$output_dir/measurement-id.txt"
		binary_tmp="$output_dir/isureal-diagnostic.tmp.$$"
		if ! ssh "$HOST" bash -s -- "$REMOTE_APP" "$STATE_DIR" > "$binary_tmp" <<'REMOTE'
set -Eeuo pipefail
app=$1
state=$2
expected=$(cat "$state/diagnostic.sha256")
[[ $(sha256sum "$app" | awk '{print $1}') == "$expected" ]] || { printf 'current application differs from this measurement\n' >&2; exit 1; }
[[ $(sha256sum "$state/diagnostic" | awk '{print $1}') == "$expected" ]] || { printf 'saved diagnostic checksum mismatch\n' >&2; exit 1; }
systemctl is-active --quiet isureal-go
pid=$(systemctl show --property=MainPID --value isureal-go)
[[ "$pid" =~ ^[1-9][0-9]*$ ]] || { printf 'service has no main process\n' >&2; exit 1; }
[[ $(sha256sum "/proc/$pid/exe" | awk '{print $1}') == "$expected" ]] || { printf 'running application differs from this measurement\n' >&2; exit 1; }
cat "$state/diagnostic"
REMOTE
		then
			rm -f -- "$binary_tmp"
			die 'could not retrieve diagnostic binary'
		fi
		mv -- "$binary_tmp" "$output_dir/isureal-diagnostic"
		date -u +%Y-%m-%dT%H:%M:%S.%NZ > "$output_dir/capture-start-utc.txt"
		ssh "$HOST" 'curl --fail --silent --show-error --max-time 10 http://127.0.0.1:6060/debug/pprof/allocs' > "$output_dir/allocs-before.pprof.tmp"
		mv -- "$output_dir/allocs-before.pprof.tmp" "$output_dir/allocs-before.pprof"
		if [[ -n "$control_dir" ]]; then
			control_tmp="$control_dir/.collector-ready.$$"
			printf '%s\n' "$output_dir" > "$control_tmp"
			mv -f -- "$control_tmp" "$control_dir/collector-ready"
		fi
		for ((window = 1; window <= windows; window++)); do
			[[ -z "$control_dir" || ! -e "$control_dir/collector-stop" ]] || break
			printf 'Capturing CPU profile %d/%d (30s)\n' "$window" "$windows"
			profile_tmp="$output_dir/cpu-$window.pprof.tmp.$$"
			if ! ssh "$HOST" 'curl --fail --silent --show-error --max-time 40 "http://127.0.0.1:6060/debug/pprof/profile?seconds=30"' > "$profile_tmp"; then
				rm -f -- "$profile_tmp"
				die "CPU profile $window failed"
			fi
			mv -- "$profile_tmp" "$output_dir/cpu-$window.pprof"
			ssh "$HOST" 'curl --fail --silent --show-error --max-time 10 http://127.0.0.1:6060/debug/pprof/allocs' > "$output_dir/allocs-after.pprof.tmp"
			mv -f -- "$output_dir/allocs-after.pprof.tmp" "$output_dir/allocs-after.pprof"
		done
		if [[ -n "$control_dir" ]]; then
			ssh "$HOST" 'curl --fail --silent --show-error --max-time 10 http://127.0.0.1:6060/debug/pprof/allocs' > "$output_dir/allocs-final.pprof.tmp"
			mv -- "$output_dir/allocs-final.pprof.tmp" "$output_dir/allocs-final.pprof"
		fi
		printf 'Profiles saved under %s\n' "$output_dir"
		;;
	restore)
		(($# == 0)) || die 'restore takes only the measurement ID'
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
restored=$(sha256sum "$app" | awk '{print $1}')
[[ "$expected_original" == "$restored" ]] || { printf 'restored binary checksum mismatch\n' >&2; exit 1; }
ready=false
for attempt in {1..20}; do
	if systemctl is-active --quiet isureal-go; then
		pid=$(systemctl show --property=MainPID --value isureal-go)
	else
		pid=
	fi
	if [[ "$pid" =~ ^[1-9][0-9]*$ ]] \
		&& [[ $(sha256sum "/proc/$pid/exe" | awk '{print $1}') == "$expected_original" ]] \
		&& [[ -z $(ss -H -ltn 'sport = :6060') ]] \
		&& [[ $(curl --silent --max-time 2 --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8080/api/me) == 401 ]]; then
		ready=true
		break
	fi
	sleep 0.5
done
[[ "$ready" == true ]] || { printf 'restored service did not pass binary, API, and pprof checks; retry restore after inspection\n' >&2; exit 1; }
printf 'original binary restored and verified; saved state retained at %s\n' "$state"
REMOTE
		;;
	-h|--help|help)
		usage
		;;
	*) die "unknown action: $action" ;;
esac
