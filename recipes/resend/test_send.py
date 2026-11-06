"""Offline safety checks. Never contact Resend or use a real credential."""

import copy
import fcntl
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import MagicMock, patch


SOURCE = Path(__file__).with_name("send.py")
SPEC = importlib.util.spec_from_file_location("resend_recipe", SOURCE)
recipe = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(recipe)
PROVIDER_ID = "9b7ad6a3-d671-4a86-a89f-b711ba574804"
SECRET = "re_TEST_ONLY_DO_NOT_USE_secret_fixture"
START = 1_790_000_000.0
BOOT_ID = "f56a3f33-40a9-4220-b68e-7213ed53d010"


class Clock:
    def __init__(self):
        self.value = START

    def __call__(self):
        return self.value


class Runtime:
    def __init__(self):
        self.boot = BOOT_ID
        self.elapsed = 3600.0

    def __call__(self):
        return self.boot, self.elapsed


class RecipeTest(unittest.TestCase):
    def setUp(self):
        # Vitest's test-temp adapter selects the checkout-owned scratch root.
        self.temporary = tempfile.TemporaryDirectory(prefix="resend-recipe-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.root.chmod(0o700)
        self.state = self.root / "logical-send"
        self.key = self.root / "resend.key"
        self.key.write_text(SECRET + "\n", encoding="utf-8")
        self.key.chmod(0o600)
        self.message = {
            "from": "development@send.example.com",
            "to": "delivered@resend.dev",
            "subject": "Synthetic development notification",
            "text": "Synthetic body with no private application information.",
            "purpose": "Verify the development notification integration",
            "recipient_kind": "simulator",
        }
        self.clock = Clock()
        self.runtime = Runtime()
        self.calls = []

    def prepare(self):
        recipe.prepare(self.message, self.state)
        return recipe.review(self.state)["approval"]

    def transport(self, payload, key, idempotency_key):
        self.calls.append((payload, key, idempotency_key))
        return 200, {"id": PROVIDER_ID}

    def send(self, approval, transport=None):
        return recipe.send(
            self.state,
            self.key,
            approval,
            now=self.clock,
            runtime=self.runtime,
            transport=transport or self.transport,
        )

    def receipt(self):
        return json.loads((self.state / "receipt.json").read_text())

    def write_receipt(self, receipt):
        (self.state / "receipt.json").write_text(json.dumps(receipt))

    def outcome(self, operation):
        try:
            return operation()
        except recipe.RecipeError as error:
            return {"error": error.code}
        except OSError:
            return {"error": "local_state_error"}

    def test_prepare_and_review_are_offline_and_inspect_omits_message(self):
        with patch.object(recipe, "post_email", side_effect=AssertionError("network")):
            approval = self.prepare()
            review = recipe.review(self.state)
            result = recipe.inspect(self.state)
        self.assertEqual(len(approval), 64)
        self.assertEqual(result["status"], "prepared")
        serialized = json.dumps(result)
        for private in (SECRET, self.message["text"], self.message["subject"], self.message["from"]):
            self.assertNotIn(private, serialized)
        self.assertIn(self.message["subject"], json.dumps(review))
        self.assertEqual(self.receipt()["payload"]["to"], [self.message["to"]])

    def test_receipt_and_identity_committed_before_dispatch(self):
        approval = self.prepare()

        def provider(payload, key, idempotency_key):
            receipt = self.receipt()
            self.assertEqual(receipt["status"], "uncertain")
            self.assertEqual(receipt["first_dispatch_at"], START)
            self.assertTrue(receipt["logical_id"])
            self.assertTrue(receipt["payload_digest"])
            self.assertEqual(json.loads(payload), receipt["payload"])
            self.assertEqual(key, SECRET)
            self.assertIn(receipt["logical_id"], idempotency_key)
            self.assertEqual((self.state.stat().st_mode & 0o777), 0o700)
            self.assertEqual(((self.state / "receipt.json").stat().st_mode & 0o777), 0o600)
            return 200, {"id": PROVIDER_ID}

        result = self.send(approval, provider)
        self.assertEqual(result["status"], "accepted")
        self.assertEqual(result["provider_id"], PROVIDER_ID)

    def test_lost_response_reuses_exact_payload_and_key_within_window(self):
        approval = self.prepare()
        accepted = {}

        def provider(payload, key, idempotency_key):
            self.calls.append((payload, key, idempotency_key))
            if idempotency_key not in accepted:
                accepted[idempotency_key] = PROVIDER_ID
                raise TimeoutError("lost response " + SECRET)
            return 200, {"id": accepted[idempotency_key]}

        result = self.send(approval, provider)
        self.assertEqual(result["status"], "uncertain")
        self.assertEqual(result["error"], "network_error")
        self.clock.value += 3600
        result = self.send(approval, provider)
        self.assertEqual(result["status"], "accepted")
        self.assertEqual(result["provider_id"], PROVIDER_ID)
        self.assertEqual(len(accepted), 1)
        self.assertEqual(self.calls[0], self.calls[1])
        self.assertEqual(self.receipt()["first_dispatch_at"], START)

    def test_failed_local_acceptance_write_retains_prior_uncertainty(self):
        approval = self.prepare()
        original_save = recipe.save
        writes = 0

        def save(state, receipt):
            nonlocal writes
            writes += 1
            if writes == 2:
                raise OSError("simulated full disk")
            original_save(state, receipt)

        with patch.object(recipe, "save", side_effect=save):
            result = self.outcome(lambda: self.send(approval))
        self.assertTrue(result.get("error"))
        self.assertEqual(self.receipt()["status"], "uncertain")
        self.assertEqual(len(self.calls), 1)
        self.clock.value += 60
        result = self.send(approval)
        self.assertEqual(result["status"], "accepted")
        self.assertEqual(self.calls[0], self.calls[1])

    def test_failed_predispatch_write_never_contacts_provider(self):
        approval = self.prepare()
        before = self.receipt()
        with patch.object(recipe, "save", side_effect=OSError("simulated full disk")):
            result = self.outcome(lambda: self.send(approval))
        self.assertTrue(result.get("error"))
        self.assertEqual(self.calls, [])
        self.assertEqual(self.receipt(), before)

    def test_failed_predispatch_fsync_does_not_send_after_local_replace(self):
        approval = self.prepare()
        original_save = recipe.save

        def save(state, receipt):
            original_save(state, receipt)
            raise OSError("simulated directory fsync failure")

        with patch.object(recipe, "save", side_effect=save):
            result = self.outcome(lambda: self.send(approval))
        self.assertTrue(result.get("error"))
        self.assertEqual(self.calls, [])
        self.assertEqual(self.receipt()["status"], "uncertain")

    def test_later_auth_rejection_cannot_resolve_original_lost_response(self):
        approval = self.prepare()
        self.send(approval, lambda *args: (_ for _ in ()).throw(TimeoutError()))
        self.clock.value += 60
        result = self.send(approval, lambda *args: (401, {"name": "invalid_api_key"}))
        self.assertEqual(result["status"], "uncertain")
        self.assertEqual(result["error"], "auth_error")
        self.clock.value += 24 * 60 * 60
        result = self.send(approval)
        self.assertEqual(result["status"], "uncertain")
        self.assertEqual(result["error"], "idempotency_window_expired")
        self.assertEqual(self.calls, [])

    def test_credential_identity_cannot_change_after_uncertain_dispatch(self):
        approval = self.prepare()

        def lost(*args):
            self.calls.append(args)
            raise TimeoutError("lost")

        self.send(approval, lost)
        before = self.receipt()
        self.key.write_text("re_ANOTHER_ACCOUNT_private_fixture")
        self.clock.value += 60
        result = self.outcome(lambda: self.send(approval))
        self.assertEqual(result["error"], "credential_changed")
        after = self.receipt()
        self.assertEqual(after["status"], "uncertain")
        self.assertEqual(after["logical_id"], before["logical_id"])
        self.assertEqual(after["key_fingerprint"], before["key_fingerprint"])
        self.assertEqual(len(self.calls), 1)
        self.assertNotIn(SECRET, json.dumps(after))

    def test_same_credential_at_another_private_path_retains_identity(self):
        approval = self.prepare()
        self.send(approval, lambda *args: (_ for _ in ()).throw(TimeoutError()))
        before = self.receipt()
        alternate = self.root / "same-account.key"
        alternate.write_text(SECRET)
        alternate.chmod(0o600)
        self.key = alternate
        self.clock.value += 60
        result = self.send(approval)
        self.assertEqual(result["status"], "accepted")
        self.assertEqual(self.receipt()["key_fingerprint"], before["key_fingerprint"])

    def test_expiry_never_dispatches_or_regenerates_identity(self):
        approval = self.prepare()

        def lost(*args):
            self.calls.append(args)
            raise TimeoutError(SECRET)

        self.send(approval, lost)
        before = self.receipt()
        self.clock.value += 24 * 60 * 60
        result = self.outcome(lambda: self.send(approval))
        self.assertEqual(result["error"], "idempotency_window_expired")
        after = self.receipt()
        self.assertEqual(after["status"], "uncertain")
        self.assertEqual(after["logical_id"], before["logical_id"])
        self.assertEqual(after["first_dispatch_at"], START)
        self.assertEqual(len(self.calls), 1)
        self.assertNotIn(SECRET, json.dumps(after))

    def test_safety_margin_blocks_retry_near_expiry(self):
        approval = self.prepare()
        self.send(approval, lambda *args: (_ for _ in ()).throw(TimeoutError()))
        self.clock.value += 24 * 60 * 60 - 30
        result = self.outcome(lambda: self.send(approval))
        self.assertEqual(result["error"], "idempotency_window_expired")
        self.assertEqual(self.calls, [])

    def test_known_acceptance_survives_expiry_and_missing_credentials(self):
        approval = self.prepare()
        self.send(approval)
        self.clock.value += 48 * 60 * 60
        self.key.unlink()
        result = self.send(approval)
        self.assertEqual(result["status"], "accepted")
        self.assertEqual(result["provider_id"], PROVIDER_ID)
        self.assertEqual(len(self.calls), 1)

    def test_known_acceptance_does_not_read_wall_clock_or_guest_runtime(self):
        approval = self.prepare()
        self.send(approval)

        def unavailable():
            raise AssertionError("known acceptance must bypass clock checks")

        result = recipe.send(
            self.state, self.key, approval,
            now=unavailable, runtime=unavailable, transport=self.transport,
        )
        self.assertEqual(result["status"], "accepted")
        self.assertEqual(result["provider_id"], PROVIDER_ID)
        self.assertEqual(len(self.calls), 1)

    def test_suspend_time_expires_retry_even_if_wall_clock_has_not(self):
        approval = self.prepare()

        def lost(*args):
            self.calls.append(args)
            raise TimeoutError("lost")

        self.send(approval, lost)
        self.clock.value += 23 * 60 * 60
        self.runtime.elapsed += 25 * 60 * 60
        result = self.send(approval)
        self.assertEqual(result["status"], "uncertain")
        self.assertEqual(result["error"], "idempotency_window_expired")
        self.assertEqual(len(self.calls), 1)
        self.assertEqual(self.receipt()["boot_id"], BOOT_ID)

    def test_changed_boot_blocks_uncertain_send_without_adopting_new_boot(self):
        approval = self.prepare()

        def lost(*args):
            self.calls.append(args)
            raise TimeoutError("lost")

        self.send(approval, lost)
        self.clock.value += 60
        self.runtime.boot = "02d0bff0-c432-4a9a-a50c-4a8b8268f2a2"
        self.runtime.elapsed = 10
        result = self.send(approval)
        self.assertEqual(result["status"], "uncertain")
        self.assertEqual(result["error"], "guest_restarted")
        self.assertEqual(self.receipt()["boot_id"], BOOT_ID)
        self.assertEqual(len(self.calls), 1)

    def test_elapsed_rollback_blocks_send_while_wall_clock_increases(self):
        approval = self.prepare()
        self.send(approval, lambda *args: (_ for _ in ()).throw(TimeoutError()))
        self.clock.value += 60
        self.runtime.elapsed -= 1
        result = self.send(approval)
        self.assertEqual(result["status"], "uncertain")
        self.assertEqual(result["error"], "clock_rollback")
        self.assertEqual(self.calls, [])

    def test_backward_clock_blocks_uncertain_replay(self):
        approval = self.prepare()
        self.send(approval, lambda *args: (_ for _ in ()).throw(TimeoutError()))
        self.clock.value -= 1
        result = self.outcome(lambda: self.send(approval))
        self.assertEqual(result["error"], "clock_rollback")
        self.assertEqual(self.calls, [])

    def test_no_live_send_without_exact_review(self):
        self.prepare()
        result = self.outcome(lambda: self.send("0" * 64))
        self.assertTrue(result.get("error"))
        self.assertEqual(self.calls, [])
        self.assertEqual(self.receipt()["status"], "prepared")

    def test_payload_or_purpose_changes_require_a_new_review(self):
        for field, value in (("payload", None), ("purpose", "Changed purpose")):
            with self.subTest(field=field):
                self.state = self.root / ("tamper-" + field)
                approval = self.prepare()
                receipt = self.receipt()
                if field == "payload":
                    receipt["payload"]["text"] = "Changed body"
                else:
                    receipt["purpose"] = value
                self.write_receipt(receipt)
                result = self.outcome(lambda: self.send(approval))
                self.assertTrue(result.get("error"))
                self.assertEqual(self.calls, [])

    def test_prepare_cannot_overwrite_an_existing_logical_send(self):
        self.prepare()
        before = (self.state / "receipt.json").read_bytes()
        result = self.outcome(lambda: recipe.prepare(self.message, self.state))
        self.assertTrue(result.get("error"))
        self.assertEqual((self.state / "receipt.json").read_bytes(), before)

    def test_missing_corrupt_and_unsafe_state_never_dispatch(self):
        approval = self.prepare()
        for content in ("{", "{}", "null", '{"version":999}'):
            with self.subTest(content=content):
                (self.state / "receipt.json").write_text(content)
                result = self.outcome(lambda: self.send(approval))
                self.assertTrue(result.get("error"))
        (self.state / "receipt.json").unlink()
        result = self.outcome(lambda: self.send(approval))
        self.assertTrue(result.get("error"))
        self.assertEqual(self.calls, [])

    def test_invalid_dispatch_schema_is_never_reinterpreted_as_prepared(self):
        approval = self.prepare()
        prepared = self.receipt()
        mutations = (
            {"first_dispatch_at": START},
            {"last_observed_at": START},
            {"provider_id": PROVIDER_ID},
            {"version": 2},
            {"version": True},
            {"unknown_future_field": "must not be adopted"},
            {"status": "unknown-future-status"},
            {"error": SECRET},
            {"logical_id": "not-a-uuid"},
            {"boot_id": BOOT_ID},
            {"first_elapsed": 10},
            {"last_elapsed": 20},
        )
        for changes in mutations:
            with self.subTest(changes=changes):
                self.write_receipt({**prepared, **changes})
                result = self.outcome(lambda: self.send(approval))
                self.assertTrue(result.get("error"))
                self.assertEqual(self.calls, [])

    def test_key_is_private_regular_guest_owned_configuration(self):
        approval = self.prepare()
        self.key.chmod(0o644)
        result = self.outcome(lambda: self.send(approval))
        self.assertTrue(result.get("error"))
        self.key.chmod(0o600)
        original = self.root / "original.key"
        self.key.rename(original)
        self.key.symlink_to(original)
        result = self.outcome(lambda: self.send(approval))
        self.assertTrue(result.get("error"))
        self.assertEqual(self.calls, [])
        self.assertEqual(self.receipt()["status"], "prepared")

    def test_missing_credential_has_no_dispatch_or_identity_change(self):
        approval = self.prepare()
        self.key.unlink()
        before = self.receipt()
        result = self.outcome(lambda: self.send(approval))
        self.assertTrue(result.get("error"))
        self.assertEqual(self.receipt(), before)
        self.assertEqual(self.calls, [])

    def test_concurrent_send_is_rejected_while_original_dispatch_holds_lock(self):
        approval = self.prepare()

        def provider(payload, key, idempotency_key):
            with (self.state / "lock").open("r+") as lock:
                with self.assertRaises(BlockingIOError):
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = self.outcome(lambda: self.send(approval))
            self.assertTrue(result.get("error"))
            self.calls.append((payload, key, idempotency_key))
            return 200, {"id": PROVIDER_ID}

        self.assertEqual(self.send(approval, provider)["status"], "accepted")
        self.assertEqual(len(self.calls), 1)

    def test_simulator_and_owner_recipient_validation(self):
        for recipient in ("delivered+signup@resend.dev", "bounced@resend.dev"):
            with self.subTest(recipient=recipient):
                message = {**self.message, "to": recipient}
                state = self.root / recipient.split("@")[0]
                recipe.prepare(message, state)
                self.assertEqual(recipe.inspect(state)["status"], "prepared")
        for recipient in ("anyone@example.com", "fake@resend.dev", "a@example.com,b@example.com"):
            with self.subTest(recipient=recipient):
                message = {**self.message, "to": recipient}
                result = self.outcome(lambda: recipe.prepare(message, self.root / "invalid-recipient"))
                self.assertTrue(result.get("error"))
        owner = {**self.message, "to": "owner@example.com", "recipient_kind": "owner-controlled"}
        recipe.prepare(owner, self.root / "owner")
        self.assertEqual(recipe.inspect(self.root / "owner")["status"], "prepared")

    def test_invalid_or_secret_bearing_message_fields_fail_before_preparation(self):
        for changed in (
            {"to": ["delivered@resend.dev", "owner@example.com"]},
            {"from": "a@example.com\r\nBcc: owner@example.com"},
            {"purpose": ""},
            {"recipient_kind": "production"},
            {"api_key": SECRET},
        ):
            with self.subTest(changed=changed):
                message = copy.deepcopy(self.message)
                message.update(changed)
                result = self.outcome(lambda: recipe.prepare(message, self.root / "invalid"))
                self.assertTrue(result.get("error"))

    def test_provider_failures_are_visible_without_raw_response_output(self):
        failures = (
            (401, {"name": "validation_error"}, "auth_error"),
            (403, {"name": "validation_error"}, "domain_error"),
            (429, {"name": "daily_quota_exceeded"}, "quota_error"),
            (429, {"name": "monthly_quota_exceeded"}, "quota_error"),
            (500, {"name": "internal_server_error"}, "provider_error"),
            (409, {"name": "invalid_idempotent_request"}, "idempotency_conflict"),
        )
        for index, (status, body, expected) in enumerate(failures):
            with self.subTest(status=status, body=body):
                self.state = self.root / ("failure-" + str(index))
                approval = self.prepare()
                body = {**body, "message": SECRET, "details": {"token": SECRET}}
                result = self.send(approval, lambda *args: (status, body))
                self.assertEqual(result["error"], expected)
                self.assertNotIn(SECRET, json.dumps(result))
                self.assertNotIn(SECRET, (self.state / "receipt.json").read_text())

    def test_malformed_success_does_not_claim_acceptance_or_echo_provider_data(self):
        for index, body in enumerate(({"id": SECRET}, {"id": None}, {}, [SECRET])):
            with self.subTest(body=body):
                self.state = self.root / ("malformed-" + str(index))
                approval = self.prepare()
                result = self.send(approval, lambda *args: (200, body))
                self.assertEqual(result["status"], "uncertain")
                self.assertTrue(result.get("error"))
                self.assertNotIn(SECRET, json.dumps(result))

    def test_transport_uses_fixed_https_endpoint_and_disables_redirects_and_proxies(self):
        response = MagicMock()
        response.code = 200
        response.read.return_value = json.dumps({"id": PROVIDER_ID}).encode()
        opener = MagicMock()
        opener.open.return_value = response
        payload = b'{"text":"synthetic"}'
        with patch.object(recipe.urllib.request, "build_opener", return_value=opener) as build:
            result = recipe.post_email(payload, SECRET, "logical-send-key")
        self.assertEqual(result, (200, {"id": PROVIDER_ID}))
        request = opener.open.call_args.args[0]
        self.assertEqual(request.full_url, "https://api.resend.com/emails")
        self.assertEqual(request.get_method(), "POST")
        self.assertEqual(request.data, payload)
        headers = {key.lower(): value for key, value in request.header_items()}
        self.assertEqual(headers["authorization"], "Bearer " + SECRET)
        self.assertEqual(headers["idempotency-key"], "logical-send-key")
        self.assertEqual(headers["content-type"], "application/json")
        self.assertEqual(opener.open.call_args.kwargs, {"timeout": 20})
        response.read.assert_called_once_with(recipe.MAX_FILE + 1)
        handlers = build.call_args.args
        self.assertEqual(len(handlers), 2)
        self.assertIsInstance(handlers[0], recipe.urllib.request.ProxyHandler)
        self.assertEqual(handlers[0].proxies, {})
        self.assertIsInstance(handlers[1], recipe.NoRedirect)
        self.assertIsNone(handlers[1].redirect_request(
            request, None, 307, "Temporary Redirect", {}, "https://other.example.com/emails",
        ))

    def test_http_error_body_is_classified_without_retaining_provider_details(self):
        approval = self.prepare()
        body = io.BytesIO(json.dumps({"name": "email_above_quota", "message": SECRET}).encode())
        failure = recipe.urllib.error.HTTPError(
            "https://api.resend.com/emails", 429, "Too Many Requests", {}, body,
        )
        opener = MagicMock()
        opener.open.side_effect = failure
        with patch.object(recipe.urllib.request, "build_opener", return_value=opener):
            result = self.send(approval, recipe.post_email)
        self.assertEqual(result["status"], "rejected")
        self.assertEqual(result["error"], "quota_error")
        self.assertNotIn(SECRET, json.dumps(result))
        self.assertNotIn(SECRET, (self.state / "receipt.json").read_text())
        self.assertEqual(opener.open.call_count, 1)

    def test_cli_error_is_sanitized_and_nonzero(self):
        self.prepare()
        result = subprocess.run(
            [sys.executable, "-B", str(SOURCE), "send", "--state", str(self.state),
             "--key-file", str(self.key), "--approve", SECRET],
            text=True, capture_output=True, check=False,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn(SECRET, result.stdout + result.stderr)
        self.assertNotIn("Traceback", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
