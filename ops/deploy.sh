#!/usr/bin/env bash
set -euo pipefail

readonly -a APP_HOSTS=(isu-asia1 isu-europe1 isu-america1)
readonly REMOTE_APP_DIR=/home/isucon/webapp/go
readonly REMOTE_GO=/home/isucon/local/go/bin/go
readonly REMOTE_ENV_FILE=/home/isucon/env.sh
readonly REMOTE_APP_ENV_FILE=/home/isucon/isureal-app-env.sh
readonly LOCAL_HOST_ENV_DIR_NAME=ops/env

usage() {
	cat <<'EOF'
Usage: ops/deploy.sh [--dry-run] [--help]

Deploy the Go application and shared application settings to all three
application servers. Host selection is intentionally not supported.

The deploy stages the source, builds it on each server, installs the local
host-specific base environment and shared application settings, atomically
replaces the binary, and restarts isureal-go. Each base file must exist under
ops/env/<host>/env.sh.
After restart it verifies both the systemd active state and the pool settings in
the running process environment. It does not test HTTP behavior. SQL and
static assets are deployed separately.

--dry-run prints the validated plan without invoking ssh or rsync.
EOF
}

die() {
	printf 'deploy.sh: %s\n' "$*" >&2
	exit 2
}

dry_run=false
while (($# > 0)); do
	case "$1" in
		--help|-h)
			usage
			exit 0
			;;
		--dry-run)
			dry_run=true
			shift
			;;
		*) die "unsupported argument: $1" ;;
	esac
done

readonly script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly repo_root=$(cd -- "$script_dir/.." && pwd)
readonly source_dir="$repo_root/webapp/go"
readonly app_env_source="$repo_root/ops/config/app.env"
readonly host_env_dir="$repo_root/$LOCAL_HOST_ENV_DIR_NAME"
[[ -d "$source_dir" ]] || die 'Go source directory not found'
[[ -f "$source_dir/go.mod" ]] || die 'go.mod not found'
[[ -f "$source_dir/cmd/isureal/main.go" ]] || die 'Go entry point not found'
[[ -f "$app_env_source" && -r "$app_env_source" ]] || die 'shared application environment file not readable'
declare -A app_settings=()
while IFS= read -r line || [[ -n "$line" ]]; do
	case "$line" in
		''|'#'*) continue ;;
		ISUREAL_DB_MAX_IDLE_CONNS=*|ISUREAL_DB_MAX_OPEN_CONNS_ASIA=*|ISUREAL_DB_MAX_OPEN_CONNS_EUROPE=*|ISUREAL_DB_MAX_OPEN_CONNS_AMERICA=*)
			key=${line%%=*}
			[[ -z "${app_settings[$key]+present}" ]] || die 'duplicate key in shared application environment file'
			app_settings[$key]=${line#*=}
			;;
		*) die 'unsupported line in shared application environment file' ;;
	esac
done < "$app_env_source"
for key in ISUREAL_DB_MAX_IDLE_CONNS ISUREAL_DB_MAX_OPEN_CONNS_ASIA ISUREAL_DB_MAX_OPEN_CONNS_EUROPE ISUREAL_DB_MAX_OPEN_CONNS_AMERICA; do
	[[ -n "${app_settings[$key]+present}" ]] || die "shared application environment file is missing $key"
done
idle_value=${app_settings[ISUREAL_DB_MAX_IDLE_CONNS]}
[[ "$idle_value" =~ ^(0|[1-9]|[12][0-9]|30)$ ]] || die 'invalid shared idle connection value; expected an integer from 0 to 30'
declare -A region_open_values=(
	[asia]=${app_settings[ISUREAL_DB_MAX_OPEN_CONNS_ASIA]}
	[europe]=${app_settings[ISUREAL_DB_MAX_OPEN_CONNS_EUROPE]}
	[america]=${app_settings[ISUREAL_DB_MAX_OPEN_CONNS_AMERICA]}
)
open_total=0
for region in asia europe america; do
	open_value=${region_open_values[$region]}
	[[ "$open_value" =~ ^([1-9]|[1-8][0-9]|90)$ ]] || die "invalid max open value for $region; expected an integer from 1 to 90"
	((open_value >= idle_value)) || die "max open connections for $region must be at least max idle connections"
	open_total=$((open_total + open_value))
done
((open_total <= 90)) || die 'regional max open connection total must not exceed 90'
declare -A host_env_sources=()
for host in "${APP_HOSTS[@]}"; do
	host_env_source="$host_env_dir/$host/env.sh"
	[[ -f "$host_env_source" && -r "$host_env_source" && ! -L "$host_env_source" ]] || die "host environment file missing or unreadable: $host"
	if ! bash -n -- "$host_env_source" >/dev/null 2>&1; then
		die "invalid shell syntax in host environment file: $host"
	fi
	host_env_mode=$(stat -c '%a' -- "$host_env_source") || die "cannot inspect permissions for host environment file: $host"
	if (( (8#$host_env_mode & 077) != 0 )); then
		die "host environment file must be private (mode 0600 or stricter): $host"
	fi
	host_env_sources[$host]=$host_env_source
done

if $dry_run; then
	printf 'Source: %s\n' "$source_dir"
	printf 'Shared settings: %s (idle=%s, total max open=%s)\n' "$app_env_source" "$idle_value" "$open_total"
	for host in "${APP_HOSTS[@]}"; do
		case "$host" in
			isu-asia1) region=asia ;;
			isu-europe1) region=europe ;;
			isu-america1) region=america ;;
		esac
		printf 'Host environment: %s -> %s\n' "${host_env_sources[$host]}" "$host:$REMOTE_ENV_FILE"
		printf 'Would build and restart isureal-go on %s (region=%s idle=%s max_open=%s)\n' "$host" "$region" "$idle_value" "${region_open_values[$region]}"
	done
	exit 0
fi

command -v rsync >/dev/null 2>&1 || die 'rsync is required'
command -v ssh >/dev/null 2>&1 || die 'ssh is required'

for host in "${APP_HOSTS[@]}"; do
	printf 'Deploying to %s...\n' "$host"
	stage=$(ssh "$host" 'mktemp -d /tmp/isureal-deploy.XXXXXX')
	[[ "$stage" =~ ^/tmp/isureal-deploy\.[A-Za-z0-9]+$ ]] || die "unexpected staging path from $host"
	if ! rsync -az \
		--exclude='._*' \
		--exclude='AGENTS.md' \
		--exclude='.env' \
		--exclude='.env.*' \
		--exclude='env.sh' \
		--exclude='/isureal' \
		--exclude='/isureal.next' \
		"$source_dir/" "$host:$stage/"; then
		ssh "$host" "rm -rf -- '$stage'" || true
		die "source transfer failed for $host"
	fi
	if ! rsync -az --chmod=F600 "${host_env_sources[$host]}" "$host:$stage/host.env.sh"; then
		ssh "$host" "rm -rf -- '$stage'" || true
		die "host environment transfer failed for $host"
	fi
	case "$host" in
		isu-asia1) region=asia ;;
		isu-europe1) region=europe ;;
		isu-america1) region=america ;;
	esac
	open_value=${region_open_values[$region]}
	if ! ssh "$host" "bash -s -- '$stage' '$REMOTE_APP_DIR' '$REMOTE_GO' '$REMOTE_ENV_FILE' '$REMOTE_APP_ENV_FILE' '$idle_value' '$open_value'" <<'REMOTE'
set -Eeuo pipefail
stage=$1
app_dir=$2
go_bin=$3
env_file=$4
app_env_file=$5
idle_value=$6
open_value=$7
env_backup=''
app_env_backup=''
env_changed=false
app_env_changed=false
app_env_existed=false
env_existed=false
env_mode=''
app_env_mode=''
env_next=''
binary_next=''

restore_file() {
	backup=$1
	destination=$2
	mode=$3
	temporary=$(mktemp "${destination}.restore.XXXXXX")
	if install -m "$mode" -- "$backup" "$temporary" && mv -f -- "$temporary" "$destination"; then
		return 0
	fi
	rm -f -- "$temporary"
	return 1
}

cleanup() {
	status=$?
	trap - EXIT
	if [[ $status -ne 0 ]]; then
		if [[ "$env_changed" == true ]]; then
			if [[ "$env_existed" == true ]]; then
				if restore_file "$env_backup" "$env_file" "$env_mode"; then
					printf 'Restored %s from %s\n' "$env_file" "$env_backup" >&2
				else
					printf 'ERROR: restore %s manually from %s\n' "$env_file" "$env_backup" >&2
				fi
			elif ! rm -f -- "$env_file"; then
				printf 'ERROR: could not remove newly installed %s\n' "$env_file" >&2
			fi
		elif [[ -n "$env_backup" ]]; then
			rm -f -- "$env_backup" || printf 'WARNING: temporary backup remains at %s\n' "$env_backup" >&2
		fi
		if [[ "$app_env_changed" == true ]]; then
			if [[ "$app_env_existed" == true ]]; then
				if restore_file "$app_env_backup" "$app_env_file" "$app_env_mode"; then
					printf 'Restored %s from %s\n' "$app_env_file" "$app_env_backup" >&2
				else
					printf 'ERROR: restore %s manually from %s\n' "$app_env_file" "$app_env_backup" >&2
				fi
			elif ! rm -f -- "$app_env_file"; then
				printf 'ERROR: could not remove newly installed %s\n' "$app_env_file" >&2
			fi
		elif [[ -n "$app_env_backup" ]]; then
			rm -f -- "$app_env_backup" || printf 'WARNING: temporary backup remains at %s\n' "$app_env_backup" >&2
		fi
	fi
	for temporary in "$env_next" "$binary_next" "${env_file}.next" "${app_env_file}.next"; do
		if [[ -n "$temporary" ]]; then
			rm -f -- "$temporary" || printf 'WARNING: temporary file remains at %s\n' "$temporary" >&2
		fi
	done
	rm -rf -- "$stage" || printf 'WARNING: staging directory remains at %s\n' "$stage" >&2
	exit "$status"
}
trap cleanup EXIT

umask 077
exec 9>/home/isucon/.isureal-deploy.lock
flock -x 9
if [[ -e "$env_file" || -L "$env_file" ]]; then
	[[ -f "$env_file" && ! -L "$env_file" ]] || { printf 'base environment path is not a regular file\n' >&2; exit 1; }
	env_existed=true
	env_mode=$(stat -c '%a' -- "$env_file")
fi
if [[ -e "$app_env_file" || -L "$app_env_file" ]]; then
	[[ -f "$app_env_file" && ! -L "$app_env_file" ]] || { printf 'shared application settings path is not a regular file\n' >&2; exit 1; }
	app_env_existed=true
	app_env_mode=$(stat -c '%a' -- "$app_env_file")
fi

cd "$stage"
"$go_bin" build -o isureal cmd/isureal/main.go

if [[ "$env_existed" == true ]]; then
	env_backup=$(mktemp "${env_file}.rollback.XXXXXX")
	install -m 0600 -- "$env_file" "$env_backup"
fi
if [[ "$app_env_existed" == true ]]; then
	app_env_backup=$(mktemp "${app_env_file}.rollback.XXXXXX")
	install -m 0600 -- "$app_env_file" "$app_env_backup"
fi
printf 'Rollback backups on this host: env=%s legacy-app-env=%s\n' "${env_backup:-none}" "${app_env_backup:-none}"

env_next=$(mktemp "${env_file}.next.XXXXXX")
awk '
/^[[:space:]]*(source|\.)[[:space:]]+\/home\/isucon\/isureal-app-env\.sh([[:space:]]|$)/ { next }
/^[[:space:]]*(export[[:space:]]+)?ISUREAL_DB_MAX_IDLE_CONNS=/ { next }
/^[[:space:]]*(export[[:space:]]+)?ISUREAL_DB_MAX_OPEN_CONNS=/ { next }
/^[[:space:]]*(export[[:space:]]+)?ISUREAL_DB_MAX_OPEN_CONNS_(ASIA|EUROPE|AMERICA)=/ { next }
{ print }
' "$stage/host.env.sh" > "$env_next"
printf '\n' >> "$env_next"
printf 'ISUREAL_DB_MAX_IDLE_CONNS=%s\nISUREAL_DB_MAX_OPEN_CONNS=%s\n' "$idle_value" "$open_value" >> "$env_next"
printf '\n' >> "$env_next"

binary_next=$(mktemp "$app_dir/isureal.next.XXXXXX")
install -m 0755 -- isureal "$binary_next"

mv -f -- "$env_next" "$env_file"
env_changed=true
if [[ "$app_env_existed" == true ]]; then
	rm -f -- "$app_env_file"
	app_env_changed=true
fi
mv -f -- "$binary_next" "$app_dir/isureal"
sudo -n systemctl restart isureal-go
systemctl is-active --quiet isureal-go
main_pid=$(systemctl show --property=MainPID --value isureal-go)
[[ "$main_pid" =~ ^[1-9][0-9]*$ ]] || { printf 'service has no running main process after restart\n' >&2; exit 1; }
actual_idle_value=''
actual_idle_count=0
actual_open_value=''
actual_open_count=0
while IFS= read -r -d '' environment_entry; do
	case "$environment_entry" in
		ISUREAL_DB_MAX_IDLE_CONNS=*)
			actual_idle_count=$((actual_idle_count + 1))
			candidate_idle_value=${environment_entry#*=}
			if [[ "$candidate_idle_value" =~ ^(0|[1-9]|[12][0-9]|30)$ ]]; then
				actual_idle_value=$candidate_idle_value
			else
				actual_idle_value=non-numeric
			fi
			;;
		ISUREAL_DB_MAX_OPEN_CONNS=*)
			actual_open_count=$((actual_open_count + 1))
			candidate_open_value=${environment_entry#*=}
			if [[ "$candidate_open_value" =~ ^([1-9]|[1-8][0-9]|90)$ ]]; then
				actual_open_value=$candidate_open_value
			else
				actual_open_value=non-numeric
			fi
			;;
	esac
done < "/proc/$main_pid/environ"
if [[ "$actual_idle_count" -ne 1 || "$actual_idle_value" != "$idle_value" || "$actual_open_count" -ne 1 || "$actual_open_value" != "$open_value" ]]; then
	printf 'running process pool setting mismatch: expected_idle=%s actual_idle=%s idle_count=%s expected_open=%s actual_open=%s open_count=%s\n' "$idle_value" "$actual_idle_value" "$actual_idle_count" "$open_value" "$actual_open_value" "$actual_open_count" >&2
	exit 1
fi
REMOTE
	then
		die "build, configuration install, or service restart failed for $host"
	fi
	printf 'Application installed; idle=%s max_open=%s verified on %s (HTTP unverified)\n' "$idle_value" "$open_value" "$host"
done
