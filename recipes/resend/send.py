#!/usr/bin/env python3
"""Explicit, single-message development sends; Python 3 standard library only."""

import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import math
import os
from pathlib import Path
import re
import stat
import sys
import time
import urllib.error
import urllib.request
import uuid


WINDOW = 24 * 60 * 60
SAFETY_MARGIN = 60
MAX_FILE = 65536
MAILBOX = re.compile(r"[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}")
SIMULATOR = re.compile(r"(?:delivered|bounced)(?:\+[A-Za-z0-9_-]+)?@resend\.dev")
ERRORS = {
    "network_error", "provider_error", "auth_error", "domain_error",
    "quota_error", "rate_limit", "idempotency_conflict", "validation_error",
    "idempotency_window_expired", "clock_rollback", "guest_restarted",
}


class RecipeError(Exception):
    """Only fixed codes are allowed in task output."""

    def __init__(self, code):
        super().__init__(code)
        self.code = code


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode()


def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()


def checked_path(path, directory=False):
    path = Path(path)
    if not path.is_absolute() or path.resolve() != path:
        raise RecipeError("private_path_required")
    info = path.lstat()
    expected = stat.S_ISDIR(info.st_mode) if directory else stat.S_ISREG(info.st_mode)
    if not expected or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise RecipeError("private_path_required")
    if not directory and info.st_nlink != 1:
        raise RecipeError("private_path_required")
    return path


def read_private(path):
    path = checked_path(path)
    checked_path(path.parent, directory=True)
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(descriptor, "rb") as handle:
        info = os.fstat(handle.fileno())
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid()
                or info.st_mode & 0o077 or info.st_nlink != 1):
            raise RecipeError("private_path_required")
        data = handle.read(MAX_FILE + 1)
    if len(data) > MAX_FILE:
        raise RecipeError("invalid_local_data")
    return data


@contextmanager
def locked(state):
    state = checked_path(state, directory=True)
    descriptor = os.open(state / "lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    try:
        info = os.fstat(descriptor)
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid()
                or info.st_mode & 0o077 or info.st_nlink != 1):
            raise RecipeError("private_path_required")
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RecipeError("send_busy") from None
        yield state
    finally:
        os.close(descriptor)


def save(state, receipt):
    # A new temporary name never adopts a file left by an interrupted write.
    temporary = state / (".receipt-" + str(uuid.uuid4()))
    descriptor = os.open(temporary, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        with os.fdopen(descriptor, "wb") as handle:
            handle.write(canonical(receipt))
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, state / "receipt.json")
        descriptor = os.open(state, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
    finally:
        temporary.unlink(missing_ok=True)


def validate_message(message):
    fields = {"from", "to", "subject", "text", "purpose", "recipient_kind"}
    if not isinstance(message, dict) or set(message) != fields:
        raise RecipeError("invalid_message")
    if any(not isinstance(value, str) or not value.strip() for value in message.values()):
        raise RecipeError("invalid_message")
    if any(not MAILBOX.fullmatch(message[key]) for key in ("from", "to")):
        raise RecipeError("invalid_message")
    for key in ("from", "to", "subject", "purpose"):
        if len(message[key]) > 256 or any(ord(char) < 32 or ord(char) == 127 for char in message[key]):
            raise RecipeError("invalid_message")
    if len(message["text"]) > 8192 or len(canonical(message)) > 32768:
        raise RecipeError("invalid_message")
    if message["recipient_kind"] == "simulator":
        if not SIMULATOR.fullmatch(message["to"]):
            raise RecipeError("invalid_simulator")
    elif message["recipient_kind"] != "owner-controlled" or message["to"].lower().endswith("@resend.dev"):
        raise RecipeError("invalid_recipient_kind")
    return {
        "from": message["from"], "to": [message["to"]],
        "subject": message["subject"], "text": message["text"],
    }


def approval_for(receipt):
    return digest({key: receipt[key] for key in (
        "logical_id", "payload", "purpose", "recipient_kind",
    )})


def prepare(message, state):
    payload = validate_message(message)
    state = Path(state)
    checked_path(state.parent, directory=True)
    if not state.is_absolute() or state.resolve() != state:
        raise RecipeError("private_path_required")
    # Existing directories, including an empty or damaged old journal, are never reset.
    try:
        state.mkdir(mode=0o700)
    except FileExistsError:
        raise RecipeError("state_already_exists") from None
    receipt = {
        "version": 1, "logical_id": str(uuid.uuid4()), "payload": payload,
        "payload_digest": digest(payload), "purpose": message["purpose"],
        "recipient_kind": message["recipient_kind"], "status": "prepared",
        "first_dispatch_at": None, "last_observed_at": None,
        "provider_id": None, "error": None, "key_fingerprint": None,
        "boot_id": None, "first_elapsed": None, "last_elapsed": None,
    }
    receipt["approval"] = approval_for(receipt)
    with locked(state):
        save(state, receipt)
    # Persist the new directory entry too, before reporting successful preparation.
    descriptor = os.open(state.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    return safe_result(receipt)


def valid_uuid(value):
    return isinstance(value, str) and str(uuid.UUID(value)) == value


def load(state):
    try:
        receipt = json.loads(read_private(state / "receipt.json"))
        fields = {
            "version", "logical_id", "payload", "payload_digest", "purpose",
            "recipient_kind", "status", "first_dispatch_at", "last_observed_at",
            "provider_id", "error", "key_fingerprint", "boot_id", "first_elapsed",
            "last_elapsed", "approval",
        }
        if not isinstance(receipt, dict) or set(receipt) != fields:
            raise ValueError()
        if type(receipt["version"]) is not int or receipt["version"] != 1 or not valid_uuid(receipt["logical_id"]):
            raise ValueError()
        payload = receipt["payload"]
        message = {**payload, "to": payload["to"][0], "purpose": receipt["purpose"],
                   "recipient_kind": receipt["recipient_kind"]}
        if validate_message(message) != payload or digest(payload) != receipt["payload_digest"]:
            raise ValueError()
        if approval_for(receipt) != receipt["approval"]:
            raise ValueError()
        if receipt["status"] not in {"prepared", "uncertain", "accepted", "rejected"}:
            raise ValueError()
        if receipt["error"] is not None and receipt["error"] not in ERRORS:
            raise ValueError()
        first, last = receipt["first_dispatch_at"], receipt["last_observed_at"]
        first_elapsed, last_elapsed = receipt["first_elapsed"], receipt["last_elapsed"]
        for value in (first, last, first_elapsed, last_elapsed):
            if value is not None and (type(value) not in (int, float) or not math.isfinite(value) or value < 0):
                raise ValueError()
        if receipt["status"] == "prepared":
            if (first is not None or last is not None or receipt["provider_id"] is not None
                    or receipt["key_fingerprint"] is not None):
                raise ValueError()
            if receipt["boot_id"] is not None or first_elapsed is not None or last_elapsed is not None:
                raise ValueError()
        elif first is None or last is None or last < first:
            raise ValueError()
        elif not isinstance(receipt["key_fingerprint"], str) or not re.fullmatch(r"[0-9a-f]{64}", receipt["key_fingerprint"]):
            raise ValueError()
        elif (not valid_uuid(receipt["boot_id"]) or first_elapsed is None
              or last_elapsed is None or last_elapsed < first_elapsed):
            raise ValueError()
        if receipt["status"] == "accepted":
            if not valid_uuid(receipt["provider_id"]) or receipt["error"] is not None:
                raise ValueError()
        elif receipt["provider_id"] is not None:
            raise ValueError()
        return receipt
    except (ValueError, KeyError, TypeError, IndexError, RecipeError):
        raise RecipeError("invalid_receipt") from None


def safe_result(receipt):
    return {key: receipt[key] for key in (
        "logical_id", "payload_digest", "status", "provider_id", "error",
        "first_dispatch_at", "last_observed_at",
    )}


def inspect(state):
    with locked(state) as state:
        return safe_result(load(state))


def review(state):
    with locked(state) as state:
        receipt = load(state)
        return {**safe_result(receipt), **{key: receipt[key] for key in (
            "payload", "purpose", "recipient_kind", "approval",
        )}}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def post_email(payload_bytes, api_key, idempotency_key):
    request = urllib.request.Request(
        "https://api.resend.com/emails", data=payload_bytes,
        headers={"Authorization": "Bearer " + api_key, "Content-Type": "application/json",
                 "Idempotency-Key": idempotency_key, "User-Agent": "axv-resend-development-recipe"},
        method="POST",
    )
    # No proxy inheritance, redirect, provider SDK retries or TLS bypass.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    try:
        response = opener.open(request, timeout=20)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        body = response.read(MAX_FILE + 1)
        if len(body) > MAX_FILE:
            raise RecipeError("provider_error")
        return response.code, json.loads(body)


def provider_error(status, response):
    name = response.get("name") if isinstance(response, dict) else None
    if status == 409:
        return "idempotency_conflict"
    if status == 401 or name in {"missing_api_key", "invalid_api_key", "restricted_api_key", "invalid_permission", "suspended_api_key"}:
        return "auth_error"
    if status == 429 or name == "email_above_quota":
        return "rate_limit" if name == "rate_limit_exceeded" else "quota_error"
    if status == 403:
        return "domain_error"
    if status in {400, 422}:
        return "validation_error"
    return "provider_error"


def runtime_clock():
    return (Path("/proc/sys/kernel/random/boot_id").read_text().strip(),
            time.clock_gettime(time.CLOCK_BOOTTIME))


def send(state, key_file, approval, *, now=time.time, transport=post_email, runtime=runtime_clock):
    with locked(state) as state:
        receipt = load(state)
        if approval != receipt["approval"]:
            raise RecipeError("approval_required")
        if receipt["status"] == "accepted":
            return safe_result(receipt)
        current = now()
        if type(current) not in (int, float) or not math.isfinite(current) or current < 0:
            raise RecipeError("invalid_clock")
        boot, elapsed = runtime()
        if (not valid_uuid(boot) or type(elapsed) not in (int, float)
                or not math.isfinite(elapsed) or elapsed < 0):
            raise RecipeError("invalid_clock")
        first, last = receipt["first_dispatch_at"], receipt["last_observed_at"]
        if receipt["boot_id"] is not None and boot != receipt["boot_id"]:
            receipt.update(error="guest_restarted")
            save(state, receipt)
            return safe_result(receipt)
        if last is not None and (current < last or elapsed < receipt["last_elapsed"]):
            receipt.update(error="clock_rollback")
            save(state, receipt)
            return safe_result(receipt)
        if first is not None and (current - first >= WINDOW - SAFETY_MARGIN
                                  or elapsed - receipt["first_elapsed"] >= WINDOW - SAFETY_MARGIN):
            # A later rejection cannot resolve an earlier lost response.
            receipt.update(error="idempotency_window_expired", last_observed_at=current)
            save(state, receipt)
            return safe_result(receipt)
        key = read_private(key_file).decode("ascii").strip()
        if not re.fullmatch(r"re_[A-Za-z0-9_-]{8,256}", key):
            raise RecipeError("invalid_key")
        fingerprint = hashlib.sha256(key.encode("ascii")).hexdigest()
        if receipt["key_fingerprint"] is not None and receipt["key_fingerprint"] != fingerprint:
            raise RecipeError("credential_changed")
        was_uncertain = receipt["status"] == "uncertain"
        receipt.update(status="uncertain", first_dispatch_at=first if first is not None else current,
                       last_observed_at=current, error=None, key_fingerprint=fingerprint,
                       boot_id=boot, first_elapsed=receipt["first_elapsed"] if first is not None else elapsed,
                       last_elapsed=elapsed)
        save(state, receipt)
        # Once dispatch might have started, any missing/malformed response is uncertain.
        try:
            status, response = transport(canonical(receipt["payload"]), key, "axv-resend/" + receipt["logical_id"])
            if status == 200 and isinstance(response, dict) and valid_uuid(response.get("id")):
                receipt.update(status="accepted", provider_id=response["id"], error=None)
            else:
                category = provider_error(status, response)
                rejected = status in {400, 401, 403, 422, 429}
                receipt.update(status="rejected" if rejected and not was_uncertain else "uncertain", error=category)
        except Exception:
            receipt.update(status="uncertain", error="network_error")
        save(state, receipt)
        return safe_result(receipt)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for command in ("prepare", "review", "inspect", "send"):
        child = commands.add_parser(command)
        child.add_argument("--state", type=Path, required=True)
        if command == "prepare":
            child.add_argument("--message", type=Path, required=True)
        if command == "send":
            child.add_argument("--key-file", type=Path, required=True)
            child.add_argument("--approve", required=True)
    arguments = parser.parse_args()
    try:
        if arguments.command == "prepare":
            # Message is synthetic application input, never credential configuration.
            with arguments.message.open("rb") as handle:
                data = handle.read(MAX_FILE + 1)
            if len(data) > MAX_FILE:
                raise RecipeError("invalid_message")
            result = prepare(json.loads(data), arguments.state)
        elif arguments.command == "send":
            result = send(arguments.state, arguments.key_file, arguments.approve)
        elif arguments.command == "review":
            result = review(arguments.state)
        else:
            result = inspect(arguments.state)
        print(json.dumps(result, sort_keys=True, ensure_ascii=True))
        return 0 if result["status"] in {"prepared", "accepted"} and result["error"] is None else 1
    except RecipeError as error:
        print(json.dumps({"error": error.code}), file=sys.stderr)
    except Exception:
        # Never print filesystem paths, provider bodies, credentials or tracebacks.
        print('{"error":"local_state_error"}', file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
