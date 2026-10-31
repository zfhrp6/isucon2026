from __future__ import annotations

import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import tempfile
import unittest


REPO_ROOT = Path(__file__).resolve().parents[2]
SCRIPT = REPO_ROOT / "ops" / "fetch-postgresql-logs.sh"
LOG_PATH = "/var/log/postgresql/postgresql-16-main.log"
LOG_CONTENT = b"2026-10-31 01:00:00 UTC [123] query duration: 150 ms\n"
CLUSTER_ROW = (
    "16\tmain\t5432\tonline\tpostgres\t/var/lib/postgresql/16/main\t"
    f"{LOG_PATH}\n"
)
SETTINGS_ROW = "\t".join(
    [
        "/etc/postgresql/16/main/postgresql.conf",
        "/var/lib/postgresql/16/main",
        "off",
        "stderr",
        "log",
        "postgresql-%Y-%m-%d_%H%M%S.log",
        "100ms",
        "%m [%p] %c %q%u@%d,%a ",
        "on",
        "on",
        "on",
        "0",
        "0",
        "1s",
        "Etc/UTC",
        "",
    ]
) + "\n"


FAKE_SSH = r'''#!/usr/bin/env python3
import os
import subprocess
import sys

args = sys.argv[1:]
host, remote_command = args[-2:]
remote_stdin = sys.stdin.buffer.read()
with open(os.environ["FAKE_SSH_CALLS"], "ab") as calls:
    calls.write(host.encode() + b"\t" + remote_command.encode() + b"\n")
if host != "isu-asia2":
    raise SystemExit(97)
remote_env = os.environ.copy()
remote_env["PATH"] = os.pathsep.join([os.environ["FAKE_REMOTE_BIN"], os.environ["FAKE_SYSTEM_PATH"]])
if remote_command == "sudo -n bash -s":
    result = subprocess.run(
        ["bash", "-s"],
        input=remote_stdin,
        env=remote_env,
        stdout=sys.stdout.buffer,
        stderr=sys.stderr.buffer,
        check=False,
    )
else:
    result = subprocess.run(
        ["bash", "-c", remote_command],
        env=remote_env,
        stdout=sys.stdout.buffer,
        stderr=sys.stderr.buffer,
        check=False,
    )
raise SystemExit(result.returncode)
'''

FAKE_PG_LSCLUSTERS = r'''#!/usr/bin/env python3
import os
import sys
sys.stdout.write(os.environ["FAKE_CLUSTER_ROW"])
'''

FAKE_STAT = r'''#!/usr/bin/env python3
import os
import sys
args = sys.argv[1:]
path = args[-1]
if path == "/var/log/postgresql/postgresql-16-main.log":
    sys.stdout.write(os.environ.get("FAKE_REMOTE_STAT", "regular file\t640\tpostgres\tadm\n"))
else:
    raise SystemExit(98)
'''

FAKE_PSQL = r'''#!/usr/bin/env python3
import os
import sys
args = sys.argv[1:]
def option_value(name):
    try:
        return args[args.index(name) + 1]
    except (ValueError, IndexError):
        return None
if option_value("-h") != "/var/run/postgresql" or option_value("-p") != "5432":
    print("psql must use the checked PostgreSQL cluster socket and port", file=sys.stderr)
    raise SystemExit(99)
with open(os.environ["FAKE_PSQL_ARGS"], "w") as out:
    out.write(" ".join(args))
sys.stdout.write(os.environ["FAKE_SETTINGS_ROW"])
'''

FAKE_SUDO = r'''#!/usr/bin/env python3
import os
import subprocess
import sys
args = sys.argv[1:]
if args[:3] == ["-n", "-u", "postgres"]:
    command = args[3:]
elif args[:2] == ["-n", "cat"]:
    sys.stdout.buffer.write(os.environ["FAKE_LOG_CONTENT"].encode())
    if os.environ.get("FAKE_SSH_FAIL_STAGE") == "log":
        raise SystemExit(22)
    raise SystemExit(0)
else:
    raise SystemExit(97)
result = subprocess.run(command, check=False)
raise SystemExit(result.returncode)
'''


class FetchPostgreSQLLogsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.temp_path = Path(self.temp_dir.name)
        self.root = self.temp_path / "repo"
        ops_dir = self.root / "ops"
        ops_dir.mkdir(parents=True)
        self.script = ops_dir / "fetch-postgresql-logs.sh"
        shutil.copy2(SCRIPT, self.script)
        self.script.chmod(0o755)

        self.local_bin = self.temp_path / "local-bin"
        self.local_bin.mkdir()
        ssh = self.local_bin / "ssh"
        ssh.write_text(FAKE_SSH)
        ssh.chmod(0o755)

        self.remote_bin = self.temp_path / "remote-bin"
        self.remote_bin.mkdir()
        self.write_executable(self.remote_bin / "pg_lsclusters", FAKE_PG_LSCLUSTERS)
        self.write_executable(self.remote_bin / "stat", FAKE_STAT)
        self.write_executable(self.remote_bin / "psql", FAKE_PSQL)
        self.write_executable(self.remote_bin / "sudo", FAKE_SUDO)

        self.system_path = os.environ["PATH"]
        self.calls_file = self.temp_path / "ssh-calls"
        self.psql_args_file = self.temp_path / "psql-args"

    @staticmethod
    def write_executable(path: Path, content: str) -> None:
        path.write_text(content)
        path.chmod(0o755)

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def run_script(self, *args: str, **extra_env: str) -> subprocess.CompletedProcess[str]:
        env = os.environ.copy()
        env.update(
            {
                "PATH": f"{self.local_bin}{os.pathsep}{self.system_path}",
                "FAKE_SYSTEM_PATH": self.system_path,
                "FAKE_REMOTE_BIN": str(self.remote_bin),
                "FAKE_SSH_CALLS": str(self.calls_file),
                "FAKE_PSQL_ARGS": str(self.psql_args_file),
                "FAKE_CLUSTER_ROW": CLUSTER_ROW,
                "FAKE_SETTINGS_ROW": SETTINGS_ROW,
                "FAKE_LOG_CONTENT": LOG_CONTENT.decode(),
            }
        )
        env.update(extra_env)
        return subprocess.run(
            [str(self.script), *args],
            cwd=self.root,
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )

    def ssh_calls(self) -> list[str]:
        if not self.calls_file.exists():
            return []
        return self.calls_file.read_text().splitlines()

    def assert_no_completed_capture(self) -> None:
        roots = list((self.root / "logs" / "postgresql").glob("*"))
        self.assertEqual(len(roots), 1)
        host_dir = roots[0] / "isu-asia2"
        self.assertFalse((host_dir / "postgresql.log").exists())
        self.assertFalse((host_dir / "settings.tsv").exists())
        self.assertEqual(list(host_dir.glob("*.part")), [])

    def test_dry_run_has_no_network_or_filesystem_side_effect(self) -> None:
        result = self.run_script("--dry-run")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("isu-asia2", result.stdout)
        self.assertEqual(self.ssh_calls(), [])
        self.assertFalse((self.root / "logs").exists())

    def test_rejects_non_target_duplicate_and_unknown_options_before_ssh(self) -> None:
        for args in (("isu-asia1",), ("isu-asia2", "isu-asia2"), ("--surprise",)):
            with self.subTest(args=args):
                result = self.run_script(*args)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.ssh_calls(), [])
                self.assertFalse((self.root / "logs").exists())

    def test_rejects_symlinked_logs_parent_before_ssh(self) -> None:
        target = self.temp_path / "external"
        target.mkdir()
        (self.root / "logs").symlink_to(target, target_is_directory=True)

        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("symlink", result.stderr)
        self.assertEqual(self.ssh_calls(), [])
        self.assertEqual(list(target.iterdir()), [])

    def test_rejects_symlinked_postgresql_root_before_ssh(self) -> None:
        logs = self.root / "logs"
        logs.mkdir(mode=0o700)
        target = self.temp_path / "external"
        target.mkdir()
        (logs / "postgresql").symlink_to(target, target_is_directory=True)

        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("symlink", result.stderr)
        self.assertEqual(stat.S_IMODE(logs.stat().st_mode), 0o700)
        self.assertEqual(self.ssh_calls(), [])
        self.assertEqual(list(target.iterdir()), [])

    def test_rejects_insecure_existing_output_root_without_chmod(self) -> None:
        logs = self.root / "logs"
        logs.mkdir(mode=0o700)
        output_root = logs / "postgresql"
        output_root.mkdir(mode=0o755)
        output_root.chmod(0o755)

        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("mode 0700", result.stderr)
        self.assertEqual(stat.S_IMODE(output_root.stat().st_mode), 0o755)
        self.assertEqual(self.ssh_calls(), [])

    def test_remote_check_accepts_off_collector_and_null_current_logfile(self) -> None:
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        match = re.search(r"under (.+)$", result.stdout, re.MULTILINE)
        self.assertIsNotNone(match)
        output_dir = Path(match.group(1)) / "isu-asia2"
        self.assertEqual((output_dir / "postgresql.log").read_bytes(), LOG_CONTENT)
        metadata = (output_dir / "settings.tsv").read_text()
        self.assertIn("logging_collector\toff\n", metadata)
        self.assertIn("pg_current_logfile_stderr\t\n", metadata)
        self.assertIn("log_parameter_max_length\t0\n", metadata)
        self.assertIn("-h /var/run/postgresql", self.psql_args_file.read_text())
        self.assertIn("-p 5432", self.psql_args_file.read_text())

    def test_fetch_saves_private_files_under_unique_run_directories(self) -> None:
        result_paths: list[Path] = []
        for _ in range(2):
            result = self.run_script()
            self.assertEqual(result.returncode, 0, result.stderr)
            match = re.search(r"under (.+)$", result.stdout, re.MULTILINE)
            self.assertIsNotNone(match)
            result_paths.append(Path(match.group(1)))

        self.assertNotEqual(result_paths[0], result_paths[1])
        output_dir = result_paths[0] / "isu-asia2"
        log_path = output_dir / "postgresql.log"
        settings_path = output_dir / "settings.tsv"
        self.assertEqual(log_path.read_bytes(), LOG_CONTENT)
        self.assertEqual(stat.S_IMODE((self.root / "logs").stat().st_mode), 0o700)
        self.assertEqual(stat.S_IMODE((self.root / "logs" / "postgresql").stat().st_mode), 0o700)
        self.assertEqual(stat.S_IMODE(result_paths[0].stat().st_mode), 0o700)
        self.assertEqual(stat.S_IMODE(output_dir.stat().st_mode), 0o700)
        self.assertEqual(stat.S_IMODE(log_path.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(settings_path.stat().st_mode), 0o600)
        self.assertEqual(len(self.ssh_calls()), 4)

    def test_failed_log_transfer_does_not_leave_completed_files(self) -> None:
        result = self.run_script(FAKE_SSH_FAIL_STAGE="log")
        self.assertNotEqual(result.returncode, 0)
        self.assert_no_completed_capture()


if __name__ == "__main__":
    unittest.main()
