"""Linux integration fixture for the installed review helper and its two sandboxes."""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


@unittest.skipUnless(
    sys.platform == "linux" and os.geteuid() == 0 and shutil.which("bwrap"),
    "requires root and bubblewrap on Linux",
)
class ReviewSandboxTest(unittest.TestCase):
    def test_mirror_and_test_command_receipts(self):
        assets = Path(__file__).resolve().parent
        with tempfile.TemporaryDirectory() as fixture:
            mirror = Path(fixture, "source.git")
            subprocess.run(["git", "init", "--quiet", "--bare", mirror], check=True)
            git = ["git", "--git-dir=" + str(mirror)]
            tree = subprocess.check_output(git + ["mktree"], text=True, input="").strip()
            commit = subprocess.check_output(
                git + ["-c", "user.name=Review", "-c", "user.email=axv@localhost", "commit-tree", tree, "-m", "fixture"],
                text=True,
            ).strip()
            subprocess.run(git + ["update-ref", "refs/heads/main", commit], check=True)
            deps = Path(fixture, "deps")
            deps.mkdir(mode=0o755)
            (deps / "dependency").write_text("present", encoding="ascii")
            outer = ["/usr/bin/bwrap", "--cap-add", "ALL", "--unshare-pid", "--unshare-net", "--dev", "/dev",
                     "--proc", "/proc", "--tmpfs", "/tmp", "--ro-bind", "/usr", "/usr", "--tmpfs", "/usr/local/lib"]
            for name in ("bin", "sbin", "lib", "lib64"):
                if Path("/usr", name).is_dir():
                    outer += ["--symlink", "usr/" + name, "/" + name]
            outer += ["--ro-bind", str(assets), "/usr/local/lib/axv",
                      "--ro-bind", str(mirror), "/opt/axv-review/mirrors/octo/demo.git",
                      "--ro-bind", str(deps), "/opt/axv-review/deps",
                      "--", "/usr/bin/python3", "-I", "/usr/local/lib/axv/review-test", "Octo/Demo", commit]
            check = """
import os
import socket
from pathlib import Path

assert os.getuid() == 65534
dependency = Path("/opt/axv-review/deps/dependency")
assert dependency.read_text() == "present"
try:
    dependency.write_text("modified")
except OSError:
    pass
else:
    raise AssertionError("fixture was writable")
Path("/tmp/test-marker").touch()
assert not Path("/var/lib/axv/tasks").exists()
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
"""
            fresh = "from pathlib import Path; raise SystemExit(1 if not Path('/tmp/test-marker').exists() else 0)"
            for command, expected in (
                (["python3", "-c", check], 0),
                (["python3", "-c", fresh], 10),
                (["missing-executable"], 1),
            ):
                with self.subTest(command=command):
                    result = subprocess.run(outer + command, capture_output=True, text=True)
                    self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                    self.assertIn("Testing commit " + commit, result.stdout)


if __name__ == "__main__":
    unittest.main()
