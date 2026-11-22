"""Only invoked as an unprivileged process inside the disposable test root."""
import os
from pathlib import Path
import subprocess
import sys


def main():
    sha, *command = sys.argv[1:]
    if os.geteuid() != 65534 or not Path("/source.pack").is_file():
        raise RuntimeError("Test isolation was not established.")
    home = Path(os.environ["HOME"])
    home.mkdir(mode=0o700)
    workspace = Path("/tmp/source")
    workspace.mkdir(mode=0o700)
    os.chdir(workspace)
    subprocess.run(["git", "init", "--quiet"], check=True)
    with open("/source.pack", "rb") as pack:
        subprocess.run(["git", "index-pack", "--stdin"], stdin=pack, stdout=subprocess.DEVNULL, check=True)
    # This checkout deliberately has no history, submodules or filters installed.
    Path(".git/shallow").write_text(sha + "\n", encoding="ascii")
    subprocess.run(["git", "-c", "core.hooksPath=/dev/null", "checkout", "--quiet", "--detach", sha], check=True)
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    if head != sha:
        raise RuntimeError("The checkout does not match the requested commit.")
    print("Testing commit " + head, flush=True)
    status = subprocess.run(command).returncode
    print("Test command exited with status " + str(status), flush=True)
    return 0 if status == 0 else 10


if __name__ == "__main__":
    try:
        sys.exit(main())
    except subprocess.CalledProcessError as exc:
        print("Git checkout failed (exit " + str(exc.returncode) + ").", file=sys.stderr)
        sys.exit(1)
    except (OSError, RuntimeError) as exc:
        print("VM test setup failed:", exc, file=sys.stderr)
        sys.exit(1)
