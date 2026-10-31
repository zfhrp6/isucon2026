#!/usr/bin/env bash
set -euo pipefail
umask 077

readonly DEFAULT_HOST=isu-asia2
readonly REMOTE_LOG=/var/log/postgresql/postgresql-16-main.log

usage() {
	cat <<'USAGE'
Usage: ops/fetch-postgresql-logs.sh [--dry-run] [--help] [isu-asia2]

Fetch the current PostgreSQL 16/main stderr log from isu-asia2. Rotated logs
are not included. The remote check requires Debian/Ubuntu pg_lsclusters to
report the online 16/main cluster, expected data/config/log paths, and
logging_collector=off with log_destination=stderr. Other layouts fail closed.

Files are stored under logs/postgresql/<UTC timestamp>-<unique id>/isu-asia2/
with private permissions. The remote operation is read-only. PostgreSQL may
append or rotate the file during transfer, so the result is not guaranteed to
be an atomic snapshot.

--dry-run prints the plan without invoking ssh or creating directories.
USAGE
}

die() {
	printf 'fetch-postgresql-logs.sh: %s\n' "$*" >&2
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

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(cd -- "$script_dir/.." && pwd -P)
logs_parent="$repo_root/logs"
logs_dir="$repo_root/logs/postgresql"
if $dry_run; then
	printf 'Would fetch the current PostgreSQL 16/main stderr log into logs/postgresql/<UTC timestamp>-<unique id>/:\n'
	printf '  %s\n' "${validated_hosts[@]}"
	exit 0
fi

for command_name in ssh date mktemp mkdir chmod mv rm stat; do
	command -v "$command_name" >/dev/null 2>&1 || die "$command_name is required"
done

validate_private_dir() {
	local path=$1 label=$2 owner mode details
	[[ ! -L "$path" ]] || die "$label must not be a symlink: $path"
	[[ -d "$path" ]] || die "$label is not a directory: $path"
	details=$(stat -c '%u %a' -- "$path") || die "could not inspect $label: $path"
	read -r owner mode <<< "$details"
	[[ "$owner" == "$EUID" && "$mode" == 700 ]] || die "$label must be owned by uid $EUID with mode 0700: $path"
}

# Inspect both existing output roots before creating anything. Never chmod an
# existing path: a shared, symlinked, or differently owned logs directory is
# an explicit configuration error rather than a place to store private logs.
for path in "$logs_parent" "$logs_dir"; do
	[[ ! -L "$path" ]] || die "output path must not be a symlink: $path"
done
if [[ -e "$logs_parent" ]]; then
	validate_private_dir "$logs_parent" 'logs parent'
else
	mkdir -m 0700 -- "$logs_parent"
	validate_private_dir "$logs_parent" 'logs parent'
fi
if [[ -e "$logs_dir" ]]; then
	validate_private_dir "$logs_dir" 'PostgreSQL logs root'
else
	mkdir -m 0700 -- "$logs_dir"
	validate_private_dir "$logs_dir" 'PostgreSQL logs root'
fi
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
result_dir=$(mktemp -d "$logs_dir/${timestamp}-XXXXXX")
validate_private_dir "$result_dir" 'new run directory'
printf 'Saving fetched PostgreSQL logs under %s\n' "$result_dir"

partials=()
cleanup_partials() {
	for partial in "${partials[@]}"; do
		[[ -z "$partial" ]] || rm -f -- "$partial"
	done
}
trap cleanup_partials EXIT

for host in "${validated_hosts[@]}"; do
	host_dir="$result_dir/$host"
	mkdir -m 0700 -- "$host_dir"
	validate_private_dir "$host_dir" 'new host directory'
	metadata_partial="$host_dir/settings.tsv.part"
	log_partial="$host_dir/postgresql.log.part"
	partials+=("$metadata_partial" "$log_partial")

	printf 'Checking PostgreSQL cluster and settings on %s...\n' "$host"
	if ! metadata=$(ssh -o BatchMode=yes -o ConnectTimeout=10 "$host" 'sudo -n bash -s' <<'REMOTE_CHECK'
set -euo pipefail
export LC_ALL=C
command -v pg_lsclusters >/dev/null 2>&1 || { echo 'pg_lsclusters is required' >&2; exit 1; }
mapfile -t cluster_rows < <(pg_lsclusters --no-header 16 main)
[[ ${#cluster_rows[@]} == 1 ]] || { echo 'expected exactly one PostgreSQL 16/main cluster' >&2; exit 1; }
read -r version cluster port status owner data_dir log_file extra <<< "${cluster_rows[0]}"
[[ -z "${extra:-}" && "$version" == 16 && "$cluster" == main && "$port" == 5432 && "$status" == online && "$owner" == postgres && "$data_dir" == /var/lib/postgresql/16/main && "$log_file" == /var/log/postgresql/postgresql-16-main.log ]] || {
	echo 'PostgreSQL 16/main does not match the supported cluster and log layout' >&2
	exit 1
}
command -v stat >/dev/null 2>&1 || { echo 'stat is required' >&2; exit 1; }
file_stat=$(stat -c $'%F\t%a\t%U\t%G' -- "$log_file")
IFS=$'\t' read -r file_type file_mode file_owner file_group <<< "$file_stat"
[[ "$file_type" == 'regular file' ]] || { echo 'expected PostgreSQL log is not a regular file' >&2; exit 1; }
settings=$(sudo -n -u postgres psql -X -qAt -v ON_ERROR_STOP=1 -F $'\t' -h /var/run/postgresql -p "$port" -d postgres -c "SELECT current_setting('config_file'), current_setting('data_directory'), current_setting('logging_collector'), current_setting('log_destination'), current_setting('log_directory'), current_setting('log_filename'), current_setting('log_min_duration_statement'), current_setting('log_line_prefix'), current_setting('log_lock_waits'), current_setting('log_connections'), current_setting('log_disconnections'), current_setting('log_parameter_max_length'), current_setting('log_parameter_max_length_on_error'), current_setting('deadlock_timeout'), current_setting('log_timezone'), coalesce(pg_current_logfile('stderr'), '')")
[[ "$settings" != *$'\n'* ]] || { echo 'unexpected multi-row PostgreSQL settings result' >&2; exit 1; }
IFS=$'\t' read -r config_file active_data_dir collector destination log_directory log_filename min_duration line_prefix lock_waits connections disconnections parameter_limit error_parameter_limit deadlock_timeout log_timezone current_logfile <<< "$settings"
[[ "$config_file" == /etc/postgresql/16/main/postgresql.conf && "$active_data_dir" == /var/lib/postgresql/16/main && "$collector" == off && "$destination" == stderr && "$min_duration" == 100ms && "$line_prefix" == '%m [%p] %c %q%u@%d,%a ' && "$lock_waits" == on && "$connections" == on && "$disconnections" == on && "$parameter_limit" == 0 && "$error_parameter_limit" == 0 ]] || {
	echo 'active PostgreSQL settings do not match the supported diagnostic configuration' >&2
	exit 1
}
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$version" "$cluster" "$port" "$status" "$owner" "$data_dir" "$log_file" "$file_stat"
printf '%s\n' "$settings"
REMOTE_CHECK
	); then
		printf 'Failed to verify PostgreSQL log source on %s; no completed log is reported under %s\n' "$host" "$result_dir" >&2
		exit 1
	fi

	mapfile -t metadata_rows <<< "$metadata"
	[[ ${#metadata_rows[@]} == 2 ]] || die "unexpected PostgreSQL metadata response from $host"
	IFS=$'\t' read -r version cluster port status owner data_dir log_file file_type mode file_owner file_group extra <<< "${metadata_rows[0]}"
	[[ -z "${extra:-}" && "$version" == 16 && "$cluster" == main && "$port" == 5432 && "$status" == online && "$owner" == postgres && "$data_dir" == /var/lib/postgresql/16/main && "$log_file" == "$REMOTE_LOG" && "$file_type" == 'regular file' ]] || die "unexpected cluster metadata from $host"
	IFS=$'\t' read -r config_file active_data_dir collector destination log_directory log_filename min_duration line_prefix lock_waits connections disconnections parameter_limit error_parameter_limit deadlock_timeout log_timezone current_logfile extra <<< "${metadata_rows[1]}"
	[[ -z "${extra:-}" && "$config_file" == /etc/postgresql/16/main/postgresql.conf && "$active_data_dir" == /var/lib/postgresql/16/main && "$collector" == off && "$destination" == stderr && "$min_duration" == 100ms && "$line_prefix" == '%m [%p] %c %q%u@%d,%a ' && "$lock_waits" == on && "$connections" == on && "$disconnections" == on && "$parameter_limit" == 0 && "$error_parameter_limit" == 0 ]] || die "unexpected PostgreSQL settings metadata from $host"

	{
		printf 'captured_at_utc\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
		printf 'host\t%s\ncluster_version\t%s\ncluster_name\t%s\ncluster_port\t%s\ncluster_status\t%s\ncluster_owner\t%s\ndata_directory\t%s\nlog_file\t%s\nlog_file_mode\t%s\nlog_file_owner\t%s\nlog_file_group\t%s\n' \
			"$host" "$version" "$cluster" "$port" "$status" "$owner" "$data_dir" "$log_file" "$mode" "$file_owner" "$file_group"
		printf 'config_file\t%s\ndata_directory_active\t%s\nlogging_collector\t%s\nlog_destination\t%s\nlog_directory\t%s\nlog_filename\t%s\nlog_min_duration_statement\t%s\nlog_line_prefix\t%s\nlog_lock_waits\t%s\nlog_connections\t%s\nlog_disconnections\t%s\nlog_parameter_max_length\t%s\nlog_parameter_max_length_on_error\t%s\ndeadlock_timeout\t%s\nlog_timezone\t%s\npg_current_logfile_stderr\t%s\n' \
			"$config_file" "$active_data_dir" "$collector" "$destination" "$log_directory" "$log_filename" "$min_duration" "$line_prefix" "$lock_waits" "$connections" "$disconnections" "$parameter_limit" "$error_parameter_limit" "$deadlock_timeout" "$log_timezone" "$current_logfile"
	} > "$metadata_partial"

	printf 'Fetching current PostgreSQL log from %s...\n' "$host"
	if ! ssh -o BatchMode=yes -o ConnectTimeout=10 "$host" "sudo -n cat -- $REMOTE_LOG" > "$log_partial"; then
		rm -f -- "$log_partial"
		printf 'Failed to fetch PostgreSQL log from %s; completed logs are not reported under %s\n' "$host" "$result_dir" >&2
		exit 1
	fi
	chmod 0600 -- "$metadata_partial" "$log_partial"
	mv -- "$metadata_partial" "$host_dir/settings.tsv"
	mv -- "$log_partial" "$host_dir/postgresql.log"
done

printf 'Fetched current PostgreSQL log and settings from selected hosts under %s\n' "$result_dir"
