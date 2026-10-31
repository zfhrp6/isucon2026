#!/usr/bin/env bash
# Stage paths are accepted only after checking the fixed mktemp pattern.
# shellcheck disable=SC2029
set -euo pipefail

readonly -a NGINX_HOSTS=(isu-asia1 isu-europe1 isu-america1)

usage() {
	cat <<'EOF'
Usage: ops/deploy-config.sh <nginx|postgresql> [--dry-run] [host ...]

nginx defaults to the three application hosts; postgresql defaults to isu-asia2.
Only the four contest hosts are allowed. Backups are retained under
/var/backups/isureal-config/.

nginx validates nginx and logrotate configuration, then reloads nginx.
PostgreSQL validates active config paths and config error views, reloads,
waits up to 15 seconds for pg_conf_load_time, and checks pending_restart.
It never restarts PostgreSQL. Restart-required changes fail preflight.
Existing sites-enabled symlinks and package-managed files stay in place.

--dry-run validates local files and prints the plan without invoking ssh/rsync.
EOF
}

die() {
	printf 'deploy-config.sh: %s\n' "$*" >&2
	exit 2
}

(($# == 1)) && [[ "$1" == --help || "$1" == -h ]] && { usage; exit 0; }
(($# > 0)) || die 'component is required (nginx or postgresql)'
component=$1
shift
case "$component" in
	nginx|postgresql) ;;
	*) die "unsupported component: $component" ;;
esac

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

if (("${#hosts[@]}" == 0)); then
	if [[ "$component" == nginx ]]; then
		hosts=("${NGINX_HOSTS[@]}")
	else
		hosts=(isu-asia2)
	fi
fi

validated_hosts=()
for host in "${hosts[@]}"; do
	case "$component:$host" in
		nginx:isu-asia1|nginx:isu-europe1|nginx:isu-america1|postgresql:isu-asia2) ;;
		*) die "host $host is not a valid target for $component" ;;
	esac
	for prior_host in "${validated_hosts[@]}"; do
		[[ "$prior_host" != "$host" ]] || die "duplicate host: $host"
	done
	validated_hosts+=("$host")
done

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
config_root="$script_dir/config"
if [[ "$component" == nginx ]]; then
	for host in "${validated_hosts[@]}"; do
		for file in nginx.conf isureal.conf logrotate.conf; do
			[[ -f "$config_root/nginx/$host/$file" ]] || die "missing config: $config_root/nginx/$host/$file"
		done
	done
else
	for file in postgresql.conf pg_hba.conf pg_ident.conf conf.d/isucon.conf; do
		[[ -f "$config_root/postgresql/isu-asia2/$file" ]] || die "missing config: $config_root/postgresql/isu-asia2/$file"
	done
fi

if $dry_run; then
	printf 'Component: %s\n' "$component"
	for host in "${validated_hosts[@]}"; do
		printf 'Would back up, install checked-in files, validate, and reload on %s\n' "$host"
	done
	exit 0
fi

command -v ssh >/dev/null 2>&1 || die 'ssh is required'
command -v rsync >/dev/null 2>&1 || die 'rsync is required'

for host in "${validated_hosts[@]}"; do
	printf 'Preparing %s configuration on %s...\n' "$component" "$host"
	stage=$(ssh "$host" 'umask 077; mktemp -d /tmp/isureal-config.XXXXXX') || die "could not create staging directory on $host"
	[[ "$stage" =~ ^/tmp/isureal-config\.[A-Za-z0-9]+$ ]] || die "unexpected staging path from $host: $stage"
	if ! ssh "$host" "mkdir -m 0700 -- '$stage/files'"; then
		ssh "$host" "rm -rf -- '$stage'" || true
		die "could not prepare staging directory on $host"
	fi

	if [[ "$component" == nginx ]]; then
		source="$config_root/nginx/$host"
		if ! rsync -a "$source/nginx.conf" "$source/isureal.conf" "$source/logrotate.conf" "$host:$stage/files/"; then
			ssh "$host" "rm -rf -- '$stage'" || true
			die "configuration transfer failed for $host"
		fi
	else
		source="$config_root/postgresql/isu-asia2"
		if ! rsync -a "$source/postgresql.conf" "$source/pg_hba.conf" "$source/pg_ident.conf" "$host:$stage/files/" ||
			! ssh "$host" "mkdir -m 0700 -- '$stage/files/conf.d'" ||
			! rsync -a "$source/conf.d/isucon.conf" "$host:$stage/files/conf.d/"; then
			ssh "$host" "rm -rf -- '$stage'" || true
			die "configuration transfer failed for $host"
		fi
	fi

	if ! ssh "$host" "sudo -n bash -s -- '$component' '$stage'" <<'REMOTE_APPLY'
#!/usr/bin/env bash
set -Eeuo pipefail
component=$1
stage=$2
[[ "$stage" =~ ^/tmp/isureal-config\.[A-Za-z0-9]+$ ]] || { echo 'invalid staging path' >&2; exit 2; }
exec 9>/run/lock/isureal-config.lock
flock -n 9 || { echo 'another configuration deployment is running' >&2; exit 1; }

backup_root=/var/backups/isureal-config
install -d -o root -g root -m 0700 "$backup_root"
backup=$(mktemp -d "$backup_root/${component}.XXXXXX")
chmod 0700 "$backup"
printf 'Backup directory: %s\n' "$backup"
changed=false
reload_attempted=false

restore_nginx() {
	failed=0
	install -o root -g root -m 0644 "$backup/nginx.conf" /etc/nginx/nginx.conf || { echo 'could not restore nginx.conf' >&2; failed=1; }
	install -o root -g root -m 0644 "$backup/isureal.conf" /etc/nginx/sites-available/isureal.conf || { echo 'could not restore isureal.conf' >&2; failed=1; }
	install -o root -g root -m 0644 "$backup/logrotate.conf" /etc/logrotate.d/nginx || { echo 'could not restore nginx logrotate policy' >&2; failed=1; }
	return "$failed"
}
restore_postgresql() {
	failed=0
	install -o postgres -g postgres -m 0644 "$backup/postgresql.conf" /etc/postgresql/16/main/postgresql.conf || { echo 'could not restore postgresql.conf' >&2; failed=1; }
	install -o postgres -g postgres -m 0640 "$backup/pg_hba.conf" /etc/postgresql/16/main/pg_hba.conf || { echo 'could not restore pg_hba.conf' >&2; failed=1; }
	install -o postgres -g postgres -m 0640 "$backup/pg_ident.conf" /etc/postgresql/16/main/pg_ident.conf || { echo 'could not restore pg_ident.conf' >&2; failed=1; }
	install -o postgres -g postgres -m 0644 "$backup/isucon.conf" /etc/postgresql/16/main/conf.d/isucon.conf || { echo 'could not restore conf.d/isucon.conf' >&2; failed=1; }
	return "$failed"
}
on_exit() {
	status=$?
	trap - EXIT
	if ((status != 0)) && $changed; then
		echo "apply failed; restoring files from $backup" >&2
		if [[ "$component" == nginx ]]; then
			restore_nginx || echo 'one or more nginx files could not be restored' >&2
			if $reload_attempted; then nginx -t && systemctl reload nginx || echo 'nginx rollback reload failed' >&2; fi
		else
			restore_postgresql || echo 'one or more PostgreSQL files could not be restored' >&2
			if $reload_attempted; then systemctl reload postgresql@16-main || echo 'PostgreSQL rollback reload failed' >&2; fi
		fi
	fi
	exit "$status"
}
trap on_exit EXIT

case "$component" in
nginx)
	[[ -L /etc/nginx/sites-enabled/isureal.conf ]] || { echo 'expected nginx site symlink is missing' >&2; exit 1; }
	[[ "$(readlink -f /etc/nginx/sites-enabled/isureal.conf)" == /etc/nginx/sites-available/isureal.conf ]] || { echo 'nginx site symlink points to an unexpected target' >&2; exit 1; }
	for entry in 'nginx.conf:/etc/nginx/nginx.conf' 'isureal.conf:/etc/nginx/sites-available/isureal.conf' 'logrotate.conf:/etc/logrotate.d/nginx'; do
		name=${entry%%:*}; dest=${entry#*:}
		[[ -f "$dest" ]] || { echo "expected file missing: $dest" >&2; exit 1; }
		cp -a -- "$dest" "$backup/$name"
	done
	changed=true
	install -o root -g root -m 0644 "$stage/files/nginx.conf" /etc/nginx/nginx.conf
	install -o root -g root -m 0644 "$stage/files/isureal.conf" /etc/nginx/sites-available/isureal.conf
	install -o root -g root -m 0644 "$stage/files/logrotate.conf" /etc/logrotate.d/nginx
	nginx -t
	logrotate --debug /etc/logrotate.d/nginx >/dev/null
	reload_attempted=true
	systemctl reload nginx
	systemctl is-active --quiet nginx
	;;
postgresql)
	config_dir=/etc/postgresql/16/main
	psql_as_postgres() { sudo -n -u postgres psql -X -qAt -v ON_ERROR_STOP=1 -d postgres "$@"; }
	paths=$(psql_as_postgres -c "SELECT current_setting('config_file') || chr(9) || current_setting('hba_file') || chr(9) || current_setting('ident_file')")
	[[ "$paths" == "$config_dir/postgresql.conf"$'\t'"$config_dir/pg_hba.conf"$'\t'"$config_dir/pg_ident.conf" ]] || { echo 'active PostgreSQL config paths differ from checked-in targets' >&2; exit 1; }
	unmanaged=$(psql_as_postgres -c "SELECT count(*) FROM pg_file_settings WHERE sourcefile IS NOT NULL AND sourcefile NOT IN ('$config_dir/postgresql.conf', '$config_dir/conf.d/isucon.conf', '/var/lib/postgresql/16/main/postgresql.auto.conf')")
	[[ "$unmanaged" == 0 ]] || { echo 'unmanaged PostgreSQL include files are active; refusing deployment' >&2; exit 1; }
	auto_settings=$(psql_as_postgres -c "SELECT count(*) FROM pg_file_settings WHERE sourcefile = '/var/lib/postgresql/16/main/postgresql.auto.conf'")
	[[ "$auto_settings" == 0 ]] || { echo 'postgresql.auto.conf contains active settings; refusing to overwrite them' >&2; exit 1; }
	for entry in 'postgresql.conf:postgresql.conf' 'pg_hba.conf:pg_hba.conf' 'pg_ident.conf:pg_ident.conf' 'isucon.conf:conf.d/isucon.conf'; do
		name=${entry%%:*}; rel=${entry#*:}; dest="$config_dir/$rel"
		[[ -f "$dest" ]] || { echo "expected file missing: $dest" >&2; exit 1; }
		cp -a -- "$dest" "$backup/$name"
	done
	baseline=$(psql_as_postgres -c 'SELECT pg_conf_load_time()::text')
	changed=true
	install -o postgres -g postgres -m 0644 "$stage/files/postgresql.conf" "$config_dir/postgresql.conf"
	install -o postgres -g postgres -m 0640 "$stage/files/pg_hba.conf" "$config_dir/pg_hba.conf"
	install -o postgres -g postgres -m 0640 "$stage/files/pg_ident.conf" "$config_dir/pg_ident.conf"
	install -d -o postgres -g postgres -m 0755 "$config_dir/conf.d"
	install -o postgres -g postgres -m 0644 "$stage/files/conf.d/isucon.conf" "$config_dir/conf.d/isucon.conf"
	errors=$(psql_as_postgres -c "SELECT (SELECT count(*) FROM pg_file_settings WHERE error IS NOT NULL) + (SELECT count(*) FROM pg_hba_file_rules WHERE error IS NOT NULL) + (SELECT count(*) FROM pg_ident_file_mappings WHERE error IS NOT NULL)")
	[[ "$errors" == 0 ]] || { echo "PostgreSQL config preflight found $errors error(s); no reload requested" >&2; exit 1; }
	unmanaged=$(psql_as_postgres -c "SELECT count(*) FROM pg_file_settings WHERE sourcefile IS NOT NULL AND sourcefile NOT IN ('$config_dir/postgresql.conf', '$config_dir/conf.d/isucon.conf', '/var/lib/postgresql/16/main/postgresql.auto.conf')")
	auto_settings=$(psql_as_postgres -c "SELECT count(*) FROM pg_file_settings WHERE sourcefile = '/var/lib/postgresql/16/main/postgresql.auto.conf'")
	[[ "$unmanaged" == 0 && "$auto_settings" == 0 ]] || { echo 'unmanaged PostgreSQL config sources appeared; no reload requested' >&2; exit 1; }
	reload_attempted=true
	systemctl reload postgresql@16-main
	loaded=false
	attempts=0
	while ((attempts < 15)); do
		attempts=$((attempts + 1))
		loaded_at=$(psql_as_postgres -c 'SELECT pg_conf_load_time()::text')
		if [[ "$loaded_at" != "$baseline" ]]; then loaded=true; break; fi
		sleep 1
	done
	$loaded || { echo 'PostgreSQL config reload not observed within 15 seconds' >&2; exit 1; }
	pending=$(psql_as_postgres -c 'SELECT count(*) FROM pg_settings WHERE pending_restart')
	[[ "$pending" == 0 ]] || { echo "$pending PostgreSQL setting(s) require restart; reload occurred but no restart was attempted" >&2; exit 1; }
	errors=$(psql_as_postgres -c "SELECT (SELECT count(*) FROM pg_file_settings WHERE error IS NOT NULL) + (SELECT count(*) FROM pg_hba_file_rules WHERE error IS NOT NULL) + (SELECT count(*) FROM pg_ident_file_mappings WHERE error IS NOT NULL)")
	[[ "$errors" == 0 ]] || { echo "PostgreSQL reported $errors config error(s) after reload" >&2; exit 1; }
	systemctl is-active --quiet postgresql@16-main
	;;
*) echo "unsupported component: $component" >&2; exit 2 ;;
esac

echo "Configuration applied and service checked; backup retained at $backup"
REMOTE_APPLY
	then
		ssh "$host" "rm -rf -- '$stage'" || true
		die "configuration apply failed on $host; inspect its retained backup and service state"
	fi
	ssh "$host" "rm -rf -- '$stage'" || die "configuration applied, but staging cleanup failed on $host: $stage"
done

printf 'Configuration deployment completed for %s target(s).\n' "${#validated_hosts[@]}"
