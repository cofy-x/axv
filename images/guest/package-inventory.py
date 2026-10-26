#!/usr/bin/env python3
"""Retain build-time OS review inputs, without deciding redistribution terms."""

import csv
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def source_uris(text, deb822):
    """Read URI positions without treating deb822 field names as URI schemes."""
    uris = []
    if deb822:
        field = None
        for line in text.splitlines():
            if not line.strip():
                field = None
            elif line.startswith("#"):
                continue
            elif line[0].isspace():
                if field == "uris":
                    uris.extend(line.split())
            else:
                match = re.fullmatch(r"([^:\s]+):[ \t]*(.*)", line)
                if not match:
                    raise ValueError("Malformed deb822 APT source field.")
                field = match[1].lower()
                if field == "uris":
                    uris.extend(match[2].split())
    else:
        for line in text.splitlines():
            active = line.split("#", 1)[0].strip()
            if not active:
                continue
            match = re.fullmatch(r"deb(?:-src)?\s+(?:\[[^\]]*\]\s+)?(\S+)(?:\s+.*)?", active)
            if not match:
                raise ValueError("Malformed one-line APT source entry.")
            uris.append(match[1])
    return uris


def collect(output, root, excluded):
    destination = output / "os-distribution"
    destination.mkdir()

    def write_json(name, value):
        (destination / name).write_text(json.dumps(value, indent=2) + "\n")

    def read_original(path, allowed):
        resolved = path.resolve(strict=True)
        if not any(resolved.is_relative_to(area.resolve()) for area in allowed):
            raise ValueError(f"Review input link escapes its allowed directory: {path}")
        if not resolved.is_file():
            raise ValueError(f"Review input must resolve to a regular file: {path}")
        return resolved.read_bytes()

    def copy_original(path, allowed):
        data = read_original(path, allowed)
        relative = path.relative_to(root).as_posix()
        if any(ord(char) < 32 or ord(char) == 127 or char == "\\" for char in relative):
            raise ValueError("Unsafe OS review input filename.")
        target = destination / "notices" / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        return {"path": "/" + relative, "file": "notices/" + relative,
                "bytes": len(data), "sha256": sha256(data)}

    apt = root / "etc/apt"
    sources = [apt / "sources.list"] if (apt / "sources.list").exists() else []
    parts = apt / "sources.list.d"
    if parts.exists():
        sources.extend(sorted(path for path in parts.iterdir()
                              if path.suffix in (".list", ".sources")))
    configurations = []
    configured_uris = []
    for path in sources:
        if path.is_symlink():
            raise ValueError("APT source configuration must not be a symlink.")
        data = read_original(path, [apt])
        text = data.decode("utf-8")
        if (re.search(r"-----BEGIN [^-\r\n]*PRIVATE KEY(?: BLOCK)?-----", text, re.I)
                or re.search(r"(?i)\b(?:password|passwd|token|secret|authorization)(?:\s+|[:=])", text)):
            raise ValueError("APT source configuration contains possible private credentials.")
        uris = source_uris(text, path.suffix == ".sources")
        # Comments are retained verbatim too; reject private URLs there as well.
        for uri in [*uris, *re.findall(r"[A-Za-z][A-Za-z0-9+.-]*://[^\s<>]+", text)]:
            parsed = urlsplit(uri)
            if (parsed.scheme not in ("http", "https") or not parsed.hostname
                    or parsed.username is not None or parsed.password is not None
                    or parsed.query or parsed.fragment):
                raise ValueError("APT source configuration requires public credential-free HTTP(S) URLs.")
        configured_uris.extend(uris)
        configurations.append({"path": "/" + path.relative_to(root).as_posix(),
                               "bytes": len(data), "sha256": sha256(data), "content": text})
    if not configurations or not configured_uris:
        raise ValueError("No APT source configurations were retained.")
    write_json("apt-sources.json", configurations)

    indexes = []
    lists = root / "var/lib/apt/lists"
    if lists.exists():
        for path in sorted(lists.iterdir()):
            if path.name in ("lock", "partial", "auxfiles"):
                continue
            if not re.fullmatch(r"[A-Za-z0-9_.%+:~-]+", path.name):
                raise ValueError("Unsafe APT index filename.")
            data = read_original(path, [lists])
            indexes.append({"path": "/" + path.relative_to(root).as_posix(),
                            "bytes": len(data), "sha256": sha256(data)})
    if not indexes:
        raise ValueError("Collect APT index identities before removing the package lists.")
    write_json("apt-indexes.json", indexes)

    docs = root / "usr/share/doc"
    common = root / "usr/share/common-licenses"
    allowed_notices = [docs, common]
    notice_name = re.compile(
        r"^(?:copyright|licen[cs]es?|copying|notices?|patents|third[-_ ]party[-_ ](?:licen[cs]es?|notices?))(?:[-_. ]|$)",
        re.I,
    )
    paths = set()

    def walk(directory, ancestors=(), retain_all=False):
        resolved = directory.resolve(strict=True)
        if not any(resolved.is_relative_to(area.resolve()) for area in allowed_notices):
            raise ValueError(f"OS notice directory link escapes its allowed directory: {directory}")
        if resolved in ancestors:
            raise ValueError(f"OS notice directory link cycle: {directory}")
        for path in sorted(directory.iterdir()):
            if path == excluded:
                continue
            if path.is_dir():
                selected = retain_all or bool(notice_name.match(path.name))
                target = path.resolve(strict=True)
                if not any(target.is_relative_to(area.resolve()) for area in allowed_notices):
                    if selected:
                        raise ValueError(f"OS notice directory link escapes its allowed directory: {path}")
                    continue
                walk(path, (*ancestors, resolved), selected)
            elif retain_all or notice_name.match(path.name) or directory.is_relative_to(common):
                paths.add(path)

    if docs.exists():
        walk(docs)
    if common.exists():
        walk(common, retain_all=True)
    with (output / "os-packages.tsv").open() as inventory:
        packages = list(csv.DictReader(inventory, delimiter="\t"))
    if not packages:
        raise ValueError("The OS package inventory is empty.")
    source_packages = {}
    for package in packages:
        name = package["Binary-Package"]
        source = package["Source-Package"]
        version = package["Source-Version"]
        if not source or not version:
            raise ValueError(f"Missing source package/version for {name}.")
        if not re.fullmatch(r"[a-z0-9][a-z0-9+.-]*(?::[a-z0-9-]+)?", name):
            raise ValueError("Invalid binary package name in OS inventory.")
        copyright_path = docs / name.split(":")[0] / "copyright"
        if len(package["Status"]) > 1 and package["Status"][1] == "i":
            if not copyright_path.exists():
                raise ValueError(f"Installed package has no copyright file: {name}")
            paths.add(copyright_path)
        source_packages.setdefault((source, version), []).append(name)
    notices = [copy_original(path, allowed_notices) for path in sorted(paths)]
    write_json("notices.json", notices)
    write_json("source-review.json", {
        "reviewRequired": True,
        "scope": "Review inputs only; no source archives downloaded or redistribution obligations approved.",
        "sourcePackages": [{"package": name, "version": version,
                            "binaryPackages": sorted(names), "reviewStatus": "pending"}
                           for (name, version), names in sorted(source_packages.items())],
    })
    checksums = []
    for path in sorted(destination.rglob("*")):
        if path.is_file():
            checksums.append(f"{sha256(path.read_bytes())}  {path.relative_to(destination).as_posix()}\n")
    (destination / "SHA256SUMS").write_text("".join(checksums))


def retain(output, root):
    if output.is_symlink() or not output.is_dir():
        raise ValueError("OS review output must be an existing real directory.")
    output = output.resolve()
    lock = output / ".os-inventory.lock"
    try:
        lock.mkdir(mode=0o700)
    except FileExistsError:
        raise ValueError("Another OS inventory collection owns the output; refusing to change it.") from None
    lock_state = lock.lstat()
    owned = []

    def remember(path):
        state = path.lstat()
        owned.append((path, state.st_dev, state.st_ino))

    def ensure_absent():
        for name in ("os-packages.tsv", "os-release", "os-distribution"):
            if os.path.lexists(output / name):
                raise ValueError("Refusing to overwrite existing guest OS review inputs.")

    try:
        ensure_absent()
        with tempfile.TemporaryDirectory(prefix=".os-inventory-", dir=output) as temporary:
            stage = Path(temporary)
            # Retain status, including held and residual packages, from the same
            # selected database as the package/source-version inventory.
            result = subprocess.run([
                "dpkg-query", "--show",
                "--showformat=${db:Status-Abbrev}\\t${binary:Package}\\t${Version}\\t${Architecture}\\t${source:Package}\\t${source:Version}\\n",
            ], check=True, stdout=subprocess.PIPE, env={**os.environ, "LC_ALL": "C"})
            if not result.stdout:
                raise ValueError("The OS package inventory is empty.")
            (stage / "os-packages.tsv").write_bytes(
                b"Status\tBinary-Package\tVersion\tArchitecture\tSource-Package\tSource-Version\n"
                + b"".join(line + b"\n" for line in sorted(result.stdout.splitlines())))
            (stage / "os-release").write_bytes((root / "etc/os-release").read_bytes())
            collect(stage, root, output)
            ensure_absent()
            # Exclusive claims never replace a file, directory or dangling link.
            # Keep the final paths compatible with both image builders.
            for name in ("os-packages.tsv", "os-release"):
                target = output / name
                os.link(stage / name, target)
                remember(target)
            target = output / "os-distribution"
            target.mkdir()
            remember(target)
            for child in (stage / "os-distribution").iterdir():
                child.rename(target / child.name)
        owned.clear()
    finally:
        for path, device, inode in reversed(owned):
            state = path.lstat()
            if (state.st_dev, state.st_ino) != (device, inode):
                raise ValueError(f"OS review output changed during cleanup; preserving it: {path}")
            if path.is_dir():
                shutil.rmtree(path)
            else:
                path.unlink()
        current_lock = lock.lstat()
        if (current_lock.st_dev, current_lock.st_ino) != (lock_state.st_dev, lock_state.st_ino):
            raise ValueError("OS inventory claim changed during collection; preserving it.")
        lock.rmdir()


if __name__ == "__main__":
    try:
        if len(sys.argv) != 2:
            raise ValueError("Usage: package-inventory.py /existing/distribution-directory")
        retain(Path(sys.argv[1]), Path(os.environ.get("DPKG_ROOT") or "/").resolve())
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        sys.exit(f"Cannot retain guest OS review inputs: {error}")
