# Dedicated GitHub Review VM

Prepare a dedicated Ubuntu Linux VM image with AXV Guest Project Tasks, `bubblewrap`, `libseccomp2`, `util-linux`, `sudo`, `git`, `python3` and the test toolchains. Reserve this guest for review work. Keep credentials out of toolchain and fixture directories. The guest kernel must support mount, PID, IPC, UTS and network namespaces and seccomp filters. Allocate at least 1 CPU and 768 MiB for the test task, plus memory for the running guest.

Install both files as root while building the dedicated image. Run these commands from this recipe directory in the image build context.

```sh
install -d -o root -g root -m 0755 /usr/local/lib/axv
install -o root -g root -m 0755 review-test /usr/local/lib/axv/review-test
install -o root -g root -m 0644 review-test-command.py /usr/local/lib/axv/review-test-command.py
printf '%s\n' 'axv ALL=(root) NOPASSWD: /usr/local/lib/axv/review-test' > /etc/sudoers.d/axv-review-test
chmod 0440 /etc/sudoers.d/axv-review-test
visudo -cf /etc/sudoers.d/axv-review-test
```

Start the VM, register an empty project on its project volume, and configure the desktop service with these environment variables. Use the IDs shown by the AXV VM service for that project and machine.

| Variable | Value |
| --- | --- |
| `AXV_REVIEW_VM_HOST_ID` | Compute host ID serving the review machine |
| `AXV_REVIEW_VM_MACHINE_ID` | Dedicated review machine ID |
| `AXV_REVIEW_VM_PROJECT_ID` | Registered VM Project ID used for task receipts and logs |

To give different repositories their own registered projects or VMs, set `AXV_REVIEW_VM_CONFIG` to an operator-owned JSON file outside the AXV checkout. Each repository entry supplies a display name and the exact Compute binding selector. Use projects served by the Compute host connected to this AXV desktop.

```json
{
  "example/api": {
    "name": "Go API tests",
    "hostId": "host-REPLACE",
    "machineId": "vm-REPLACE",
    "projectId": "project-REPLACE"
  }
}
```

Repository names match case-insensitively. The three environment variables in the table supply the default for repositories without an entry. A mapped repository only uses its own selection. Provision the mirror, toolchain and operator-owned test assets on each selected VM. Settings shows the registered environment name. Compute verifies the project and machine when AXV prepares an execution. Use an absolute registry path and restart the desktop to load a changed file. A malformed or unreadable registry makes GitHub Review unavailable and records a startup diagnostic. Keep the old Compute project available while AXV is observing its existing tasks.

The source preparation step fetches a single commit over credential-free HTTPS from the public GitHub repository. For private repositories or offline use, provision a root-owned readable bare mirror containing the required commits at `/opt/axv-review/mirrors/<lowercase-owner>/<lowercase-repo>.git`. Keep that mirror current using an independent trusted provisioning process. A configured mirror is authoritative; a missing commit produces a setup error. The command receives a depth-one checkout of the selected commit. Submodules and Git LFS objects require separate provisioning.

Tests run with UID 65534 in a fresh filesystem and PID, IPC, UTS and network namespaces, with loopback available. They see read-only system tools under `/usr`, an empty temporary home, their checkout and any read-only fixture tree provided at `/opt/axv-review/deps`. The task omits the guest project volume, account home, credentials and guest task journal. Its syscall filter restricts socket families and further namespace creation. Build shared language runtimes into `/usr` or `/usr/local`. Keep repository-specific fixtures, tools and operator-owned test scripts in a separate directory you maintain. Provision the needed files under `/opt/axv-review/deps` on the guest image or on the dedicated VM before running reviews. Keep these files read-only during test execution and reference their guest paths in the repository's test configuration. Place a Go toolchain in `/usr/local/go` and any required modules in file proxy format under `/opt/axv-review/deps/go-proxy`. Tests use the local toolchain and resolve dependencies offline; use pinned project checksums.

In GitHub Review configure a single command or an ordered list of test profiles for the repository. Each profile specifies its command, a required regular file at the PR head, and optional paths that select it when changed. Provision the toolchains for every configured command. When the desktop verifies that no test applies, it records the decision before scheduling a guest task. The PR detail keeps the selection reason with the attempt. Compute bounds the whole operation to ten minutes, one CPU, 768 MiB, 128 processes and 1 MiB of combined output. The review detail keeps the latest 16 KiB of the cached log. Reaching a resource limit, a missing executable, or unavailable source produces an infrastructure error. A completed command with nonzero exit status produces a failed test. Its actual exit status appears in the task output. Each explicit test retry creates a distinct review attempt with fresh scratch space and its own Compute receipt.

To verify the helper on a Linux build machine with root, bubblewrap and namespace support, run `python3 -I recipes/github-review/test_review_vm.py` from the repository root. The fixture builds a local synthetic Git commit, mounts read-only dependencies, and checks the receipts for a passing command, a failing command and an unavailable executable.

## Compare candidate revisions

A repository can configure assertion runtimes by setting the guest executable and its fixed arguments. Point to an interpreter or a trusted launcher installed in the VM. For example, choose `["python3", "-c"]` for Python source. The operator confirms the assertion and AXV appends its exact text as the final argument. A repository without a configured launcher uses the guest's POSIX shell.

The common ancestor and PR head use independent checkouts in the same VM boot. Both commits need to be available to the source preparation step. Provision shared dependencies for both revisions and keep the assertion's expected behavior the same for the two runs. The candidate records each checkout, result and Compute receipt. See [Using GitHub Review](../../docs/usage.md#github-review).

## Use an external script

Save this example as `check-file.sh` in a directory you maintain outside the AXV checkout. The script checks for a nonempty file using a path relative to the checked-out revision.

```sh
#!/bin/sh
if [ "$#" -ne 1 ] || [ ! -f "$1" ] || [ ! -s "$1" ]; then
  printf '%s\n' 'Expected one nonempty file in the checkout.' >&2
  exit 1
fi
printf 'Verified nonempty file %s\n' "$1"
```

Transfer the script to the dedicated review VM. From the transferred script's directory, install it as root in a versioned dependency directory.

```sh
install -d -o root -g root -m 0755 /opt/axv-review/deps/example-v1
install -o root -g root -m 0644 check-file.sh /opt/axv-review/deps/example-v1/check-file.sh
```

In **GitHub Review → Settings**, select a watched repository with `README.md` at the PR head. Choose **Add test profile** and **Add runtime**, then enter each command argument on its own line.

| Setting | Value |
| --- | --- |
| **Quick tests** · **Test name** | `Nonempty README` |
| **Quick tests** · **Required entry file** | `README.md` |
| **Quick tests** · **Test command** | `/bin/sh`, `/opt/axv-review/deps/example-v1/check-file.sh`, `README.md` |
| **Comparison runtimes** · **Runtime name** | `Nonempty file` |
| **Comparison runtimes** · **Launcher** | `/bin/sh`, `/opt/axv-review/deps/example-v1/check-file.sh` |

Save the policy. In a candidate's **Compare revisions** action, choose **Nonempty file** and enter `README.md` as the assertion. AXV appends it as the script's only argument and records the exit code and output from each checkout. When you change an external script, install it in a new version directory and update the configured paths. Keep the version and checksum with your provisioning records.
