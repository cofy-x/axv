# Resend development email recipe

Use [send.py](send.py) for one explicitly reviewed, synthetic transactional email from a user-owned development application. It needs Python 3 on a compatible Linux guest and no third-party Python packages. Start with Resend's simulator; send to a mailbox you control only when a separate inbox check is necessary. This is optional project code, not AXV authentication, workstation notifications or an email control channel for agents. It does not support lists, production recipients, inbound commands or autonomous send loops. Installed HTML applications gain no network capabilities from this recipe.

## Set up the development sender

Provider facts were checked on **2026-11-06**; review the linked documentation and your account before sending. Resend's [Free transactional plan](https://resend.com/pricing) currently includes 3,000 emails per month, 100 per day and three domains. Domain registration and DNS access are external prerequisites; a free plan does not pay for a domain or guarantee inbox placement.

1. Use a development Resend account/team and a domain you own. Prefer a dedicated development sending subdomain, such as `dev-mail.example.com`, to isolate its reputation. Add it in Resend's Domains dashboard and select its region.
2. Follow [domain verification](https://resend.com/docs/add-a-domain): copy the exact generated DKIM and SPF records into your DNS provider, including their names, types and values. Depending on the configuration, these use TXT and MX or CNAME records. Leave verification CNAME records unproxied. Preserve unrelated MX/SPF records; do not replace your mailbox's routing. Wait for verified status and review a suitable DMARC policy with your DNS owner. Sending does not require enabling inbound email.
3. Create a dedicated key in the [API Keys dashboard](https://resend.com/docs/dashboard/api-keys/introduction), choose **Sending access**, and restrict it to the verified development domain. The API calls this permission [`sending_access`](https://resend.com/docs/api-reference/api-keys/create-api-key). Confirm the actual restriction before use; a selector in a local file is not an authorization boundary. This permission does not provide general email-history reads and may include provider inbox write operations. Do not broaden it to Full access merely to inspect a send.

The default `onboarding@resend.dev` sender is [restricted to the account's own email address](https://resend.com/docs/knowledge-base/403-error-resend-dev-domain); it is not a general verified sender. This recipe's normal path uses your verified domain and its restricted key even for simulator checks. Resolve account restrictions through Resend instead of substituting recipients or widening permissions.

## Keep credentials inside the guest

First review the registered project and its exact VM. Follow [guest-owned credential handling](../../docs/usage.md#credentials-for-project-tasks), including the shared guest-account trust boundary. Copy the reviewed recipe source into the guest using [AXV SSH/file transfer](../../docs/startup.md#ssh-file-transfer-and-forwarding), for example to `/home/axv/.local/lib/axv-resend/send.py`. Source may also live in your application's repository; credentials and send journals must stay on the guest root disk, outside repositories and retained project volumes.

For the standard `axv` account, create private directories in **Guest Terminal**:

```sh
umask 077
mkdir -p /home/axv/.config/axv-resend-dev
mkdir -p /home/axv/.local/state/axv-resend-dev
chmod 700 /home/axv/.config/axv-resend-dev
chmod 700 /home/axv/.local/state/axv-resend-dev
```

Provision the key as a `0600` regular file owned by that account, such as `/home/axv/.config/axv-resend-dev/api-key`. A trusted private secret tool may write it, or use this non-echoing prompt in Guest Terminal after reviewing the code:

```sh
python3 - <<'PY'
import getpass
import os

value = getpass.getpass("Development Resend key: ")
descriptor = os.open(
    "/home/axv/.config/axv-resend-dev/api-key",
    os.O_WRONLY | os.O_CREAT | os.O_EXCL,
    0o600,
)
with os.fdopen(descriptor, "w") as output:
    output.write(value + "\n")
PY
```

This intentionally refuses to overwrite an existing file. Custom images must use the actual guest-service account and absolute paths. Do not paste the key into task argv, `export` commands, AXV documents, source files, frontend bundles or issue/PR evidence. Do not enable shell tracing, environment dumps, HTTP debugging or verbose application logs. Terminal-only environment changes do not configure an independent task. Private file modes do not isolate a key from other code running as the same guest account; use separate VMs for separate trust domains.

## Prepare and review without sending

In Guest Terminal, create `/home/axv/.config/axv-resend-dev/message.json` with exactly these fields. Replace the example sender with an address on your verified development domain. Keep subject, body and purpose synthetic and free of credentials, personal data and real account links.

```json
{
  "from": "test@dev-mail.example.com",
  "to": "delivered+axv-first@resend.dev",
  "subject": "AXV development email check",
  "text": "Synthetic development email. No account action is required.",
  "purpose": "Verify simulator acceptance for the development application",
  "recipient_kind": "simulator"
}
```

`from` and `to` are single plain mailbox strings, without display names or recipient lists. The simulator mode permits `delivered@resend.dev` and `bounced@resend.dev`, with optional labels. Resend [simulator addresses](https://resend.com/docs/dashboard/emails/send-test-emails) exercise delivery/bounce events without delivering to a real recipient; they still consume sending quota.

```sh
/usr/bin/python3 /home/axv/.local/lib/axv-resend/send.py prepare \
  --message /home/axv/.config/axv-resend-dev/message.json \
  --state /home/axv/.local/state/axv-resend-dev/simulator-delivered-001
/usr/bin/python3 /home/axv/.local/lib/axv-resend/send.py review \
  --state /home/axv/.local/state/axv-resend-dev/simulator-delivered-001
```

`prepare` is local validation/dry-run: it creates a new private state directory and journal with the logical send identity, frozen message and payload digest before any network request. It refuses an existing state directory. `review` displays the sender, recipient, subject, body, purpose and approval digest. Review all of them in Guest Terminal; do not run the content-displaying command as a durable task or copy its output into AXV documents. A digest identifies the reviewed content; it does not prove that a person reviewed it or owns a mailbox.

Preparation and review require no API key and send nothing. Keep the state directory intact: missing or corrupt established state fails instead of inventing a new identity. Editing the original message JSON after preparation does not change the frozen send.

## Send once through a durable task

On the AXV host, capture the intended [project binding](../../docs/startup.md#vm-project-tasks), then submit one bounded task. Replace `<approval-digest>` with the exact value from review; it is non-secret. Preserve the binding file, request ID and original task/attempt receipt.

```sh
axv vm project binding <project-id> > resend-binding.json
axv vm project task submit <project-id> \
  --binding-file resend-binding.json --request-id resend-delivered-001 \
  --timeout 2m --max-log-bytes 4096 -- \
  /usr/bin/python3 /home/axv/.local/lib/axv-resend/send.py send \
  --state /home/axv/.local/state/axv-resend-dev/simulator-delivered-001 \
  --key-file /home/axv/.config/axv-resend-dev/api-key \
  --approve <approval-digest>
axv vm project task receipt <project-id> resend-delivered-001 \
  --binding-file resend-binding.json
axv vm project task inspect <project-id> <task-id> \
  --binding-file resend-binding.json
axv vm project task logs <project-id> <task-id> <attempt-id> \
  --binding-file resend-binding.json --offset 0 --limit 4096
```

The sample reads the key inside the guest and sends one HTTPS request to the fixed Resend endpoint with normal certificate verification. It does not inherit proxy settings, follow redirects or retry automatically. Its safe output contains status/error categories and, when known, the provider message ID; it omits message contents, key material and raw provider responses. `inspect --state <absolute-directory>` is also safe for retained task output. Review [AXV's task acceptance contract](../../docs/api/compute.md#vm-project-tasks): task acceptance, successful command exit, provider API acceptance and inbox delivery are separate outcomes.

An accepted result records Resend's message ID. Inspect that exact ID in the provider dashboard using your normal owner login, without granting the sending key read access. For the delivered simulator, verify the simulated delivery event; for a separately prepared bounced simulator message, verify the bounce event. A bounce can follow successful API acceptance. The recipe does not ingest webhooks, read mail or promote dashboard observations into delivery claims.

Only if an actual inbox check is needed, prepare a separate synthetic message with `recipient_kind: "owner-controlled"` and one mailbox you control. Setting that field and passing the reviewed digest attest to your explicit choice. Review sender, exact recipient, subject and purpose again before submitting its own task. The program cannot establish mailbox ownership; do not use this option for arbitrary or production recipients.

## Recover the original send

If a task response is lost, inspect the **original** request receipt, exact attempt logs, local journal and provider dashboard first. A new AXV attempt is another execution: AXV idempotency does not deduplicate external effects across attempts.

Resend [retains idempotency keys for 24 hours](https://resend.com/docs/dashboard/emails/idempotency-keys). The journal preserves one idempotency key, the exact payload and a fingerprint of the original API credential. It records dispatch before networking and serializes access. A crash or lost response therefore leaves an uncertain result rather than proving that no email was sent. A replacement API credential is rejected even if it belongs to the same provider account; rotating a key does not establish that the original send failed.

The send path requires a Linux guest. It records the Linux boot identity, wall time and `CLOCK_BOOTTIME`, which includes suspended time. Replays must remain within 24 hours of the first dispatch on both clocks, with a 60-second safety margin. Observed clock rollback or a different boot blocks another dispatch. Guest restart never rebases the window, even if the remaining wall time appears sufficient: inspect the provider dashboard and make an explicit operator decision. Correct guest time remains a prerequisite; these checks do not prove elapsed real time through arbitrary VM migration, clock distortion or state tampering.

Within the remaining window and original guest boot, after the original attempt is final and its outcome has been inspected, explicitly choose whether to retry that original task specification with the same state, payload, API credential and approval digest. Preserve its original environment; review any changed binding separately. Do not submit overlapping attempts, generate a new state directory or rotate keys merely to work around uncertainty. A repeated accepted result returns the recorded acceptance without dispatching again, even after expiry, guest restart or credential removal/replacement.

After the window expires, an unresolved send stays uncertain and cannot be retried by this recipe. Inspect the provider dashboard and any recipient evidence, then record an explicit operator decision before preparing a genuinely new logical send. Missing history is not proof of non-delivery. There is no duplicate-prevention guarantee after expiry; do not edit timestamps, delete the journal, or reuse its key with different content. A verified accepted result remains accepted after expiry. External verified delivery observations also remain valid; expiry does not turn them back into unknown outcomes.

Authentication, sender/domain, daily or monthly quota, rate-limit, provider and network failures produce visible sanitized categories. Use Resend's [error reference](https://resend.com/docs/api-reference/errors) and your dashboard to investigate. Repair configuration explicitly; quota resets or a fixed key do not authorize an automatic send. Ambiguous transport/server failures require the uncertainty workflow above. Do not print raw provider errors, which may contain recipient or message details.

Local safeguards report `credential_changed`, `guest_restarted`, `clock_rollback` or `idempotency_window_expired` and exit unsuccessfully without another send. These failures preserve the original receipt and do not resolve an uncertain provider outcome. Inspect the original evidence; do not edit the fingerprint, boot identity or clocks to bypass them.

## Verify and clean up

Run the focused recipe checks from the AXV checkout, then the [required repository verification](../../docs/development.md#verification) for source changes:

```sh
pnpm exec vitest run scripts/resend-recipe.test.mjs
```

Recipe fixtures run on Unix hosts with Python 3 and inject the provider transport and clock/boot observations; production sends require the Linux guest facilities above. They cover local validation, sanitized errors, authentication/domain/quota failures, simulator requests, one logical send after a lost response within the window, expiry, backward clocks, credential changes, guest restart, known acceptance after expiry, concurrent calls and persistence failure before dispatch. Fixtures establish local behavior, not live provider acceptance.

For live acceptance, use a compatible guest and the reviewed development account/domain. Retain only sanitized evidence: source revision, guest/Python versions, original project/task/attempt identities, logical send digest, provider message IDs and observed outcomes. Run delivered and bounced simulator cases separately, inspect both dashboard events, and inspect the original task output for leaks. A real owner-mailbox check is a separate reviewed action, never an automated repeated acceptance loop. Record missing account, domain, guest, simulator or mailbox checks individually as unavailable; do not claim live delivery from a fixture or an API success response.

Stop acceptance-owned work before cleanup. Revoke the dedicated development key through Resend, then verify denial using the provider controls or a separately reviewed simulator check; do not resend an accepted journal to test revocation. Remove only this recipe's private key/configuration and resolved test state after preserving needed non-secret receipts. Preserve unresolved journals until inspection and an explicit decision. Remove development DNS records or the Resend domain only after checking they serve no other application. Ending a run, replacing a VM or deleting local files does not revoke remote credentials or remove unrelated provider resources. Root-disk credentials and journals do not travel with project-volume backups; after replacement or restore, review the environment and original outcomes before explicitly reconfiguring them.
