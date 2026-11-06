# Upstash development cache and rate-limit recipe

Use [sample.mjs](sample.mjs) for a bounded, server-side Redis cache and rate-limit check in a registered VM project. The official clients are pinned in [package.json](package.json) and the [workspace lockfile](../../pnpm-lock.yaml): `@upstash/redis` **1.41.0** and `@upstash/ratelimit` **2.2.0**. They belong to this optional recipe, not AXV's desktop runtime or task persistence. This recipe excludes automatic provisioning, QStash, Workflow, Vector and network capabilities for installed HTML applications. A local Redis deployment remains an alternative.

The locked third-party dependencies are confined to this recipe workspace and are not embedded in the AXV application distribution:

| Package | Version | Upstream source | License |
| --- | --- | --- | --- |
| `@upstash/redis` | 1.41.0 | [upstash/redis-js](https://github.com/upstash/redis-js) | MIT |
| `@upstash/ratelimit` | 2.2.0 | [upstash/ratelimit-js](https://github.com/upstash/ratelimit-js) | MIT |
| `@upstash/core-analytics` (transitive) | 0.0.10 | [upstash/core-analytics](https://github.com/upstash/core-analytics) | MIT, declared in package metadata |
| `uncrypto` (transitive) | 0.1.3 | [unjs/uncrypto](https://github.com/unjs/uncrypto) | MIT |

The installed Redis, rate-limit and uncrypto packages include their MIT license texts. Core-analytics declares MIT in its published metadata but supplies no standalone license file; preserve that distinction when reviewing redistribution.

## Review the database and guest

Provider terms were checked on **2026-11-06**; confirm your account's current terms before use. The [Free plan](https://upstash.com/pricing/redis) currently includes one database, 250 MB storage, 500,000 commands per month and 10 GB monthly bandwidth. Choose an account-owned, claimed development database and a region near the guest application. Accountless quick-start databases are deleted after three days unless claimed; this recipe does not create one. [Claimed free databases](https://upstash.com/docs/redis/help/faq) can be archived after at least 30 days of inactivity, with restoration into a new database. Restoration requires a new identity review, not an automatic endpoint substitution.

Keep billing manual. [Adding the first payment method](https://upstash.com/docs/redis/overall/billing) moves every free account resource to its paid plan; do not add a card or enable auto-upgrade to make a failed test pass. A paid database budget is not an account-wide spending limit. Hosted latency, outages and command quotas remain application constraints.

The cache and limiter need write permission. The default [Standard REST token](https://upstash.com/docs/redis/features/restapi) controls the entire database; Read Only is insufficient. [Redis ACL](https://upstash.com/docs/redis/features/security) is documented for paid databases. Use a dedicated development database when narrower provider permissions are unavailable. Key prefixes avoid collisions; they do not restrict a token's authority.

Use a compatible Linux guest with **Node 24.16.0** and **pnpm 12.11.2**, normal TLS trust and HTTPS access to the reviewed database. Standard AXV guest images do not promise Node/pnpm: prepare a reviewed image or explicit guest-user installation first. Do not silently install tools on VM Start or run the application on the host. Examples below assume `/usr/local/bin/node` and `/usr/local/bin/pnpm` work for the independent guest service, without terminal-only PATH changes.

## Configure privately and prepare offline

Follow [guest-owned credential handling](../../docs/usage.md#credentials-for-project-tasks). In Guest Terminal, create private `0700` directories on the guest root disk, such as `/home/axv/.config/axv-upstash-dev` and `/home/axv/.local/state/axv-upstash-dev`. Use the actual service account for custom images. Store the config as a `0600` regular file owned by that account, outside repositories and retained volumes. All code executing as that guest account shares its trust domain.

Create `/home/axv/.config/axv-upstash-dev/config.json` with exactly `database_id`, `url` and `token`. Obtain the ID and HTTPS REST endpoint from the same reviewed database; use a non-echoing prompt or reviewed private secret tooling. Enter the endpoint as its exact HTTPS origin, `https://<database-host>.upstash.io`, without a trailing slash, port, user information, path, query or fragment. If the console copy includes a final `/`, explicitly remove that slash before preparation. The sample does not normalize or replace an established endpoint. For example, after creating the private parent directory:

```sh
python3 - <<'PY'
import getpass
import json
import os

config = {
    "database_id": input("Reviewed database ID: "),
    "url": getpass.getpass("Database HTTPS REST origin (no final slash): "),
    "token": getpass.getpass("Development REST token: "),
}
descriptor = os.open(
    "/home/axv/.config/axv-upstash-dev/config.json",
    os.O_WRONLY | os.O_CREAT | os.O_EXCL,
    0o600,
)
with os.fdopen(descriptor, "w") as output:
    json.dump(config, output)
PY
```

This refuses an existing file. Keep values out of task argv, AXV documents/history, copied command examples, agent prompts, frontend bundles and diagnostics. Do not enable shell tracing, environment dumps or raw SDK error logging. The sample reads the private file explicitly; it does not fall back to environment variables or another database.

Record the non-secret provider identity, chosen region and development application identity in project notes. Save a binding JSON with exactly these fields, replacing the placeholders after checking the console:

```json
{
  "database_id": "reviewed-database-id",
  "region": "reviewed-region",
  "application_id": "axv-cache-development"
}
```

With the complete AXV checkout at `/projects/upstash-demo/axv`, prepare and review in Guest Terminal:

```sh
/usr/local/bin/node /projects/upstash-demo/axv/recipes/upstash/sample.mjs prepare \
  --binding /projects/upstash-demo/upstash-binding.json \
  --config /home/axv/.config/axv-upstash-dev/config.json \
  --state /home/axv/.local/state/axv-upstash-dev/check-001 --max-requests 12
/usr/local/bin/node /projects/upstash-demo/axv/recipes/upstash/sample.mjs review \
  --state /home/axv/.local/state/axv-upstash-dev/check-001
```

Preparation/review/inspection are offline and do not load the SDK. Preparation creates a new private directory with a fresh test UUID, the reviewed binding, exact sample keys, endpoint digest, credential fingerprint and frozen request budget. It refuses existing state rather than resetting or adopting it. Review the database, region, application, keys, budget and approval digest. Endpoint values and tokens are never displayed. The region/ID are your recorded assertions; the recipe does not query the provider's management API to prove them. Confirm their association with the endpoint independently in the console.

## Install and verify through durable tasks

Use [AXV project tasks](../../docs/startup.md#vm-project-tasks) on the host, retaining the original project binding, request IDs and attempt receipts. These commands assume `/projects/upstash-demo` is the registered project root. Install from its complete `axv` workspace checkout, preserving the pinned lockfile:

```sh
axv vm project binding <project-id> > upstash-task-binding.json
axv vm project task submit <project-id> \
  --binding-file upstash-task-binding.json --request-id upstash-install-001 \
  --directory axv --timeout 10m -- /usr/local/bin/pnpm install --frozen-lockfile
```

Inspect that original task until it succeeds before submitting verification. Run the installer from the complete workspace root with its existing lockfile; do not create a separate unpinned install in `recipes/upstash`. Installation output is retained: use ordinary public registry configuration without credentials in command arguments or URLs. Replace `<approval-digest>` with the reviewed value:

```sh
axv vm project task submit <project-id> \
  --binding-file upstash-task-binding.json --request-id upstash-verify-001 \
  --timeout 2m --max-log-bytes 8192 -- \
  /usr/local/bin/node /projects/upstash-demo/axv/recipes/upstash/sample.mjs verify \
  --state /home/axv/.local/state/axv-upstash-dev/check-001 \
  --config /home/axv/.config/axv-upstash-dev/config.json \
  --approve <approval-digest>
axv vm project task receipt <project-id> upstash-verify-001 \
  --binding-file upstash-task-binding.json
axv vm project task inspect <project-id> <task-id> \
  --binding-file upstash-task-binding.json
axv vm project task logs <project-id> <task-id> <attempt-id> \
  --binding-file upstash-task-binding.json --offset 0 --limit 8192
```

The sample validates the recorded database ID, endpoint digest and credential fingerprint before requesting anything. It uses synthetic values and two exact keys namespaced by application/test identity, a **5-second** cache TTL, and a **10-second** token bucket with a burst of two. One bounded verification checks the cache hit, two allowed requests followed by denial, cache expiry and token refill; it normally takes about 15 seconds including waits. It saves a running receipt before dispatch and a terminal result afterward. `inspect --state <absolute-directory>` prints safe status without credentials or cached values, including while another operation holds the state lock.

The policy is **fail closed**: provider/network/authentication/quota errors and malformed results never authorize application work. The [rate-limit SDK's timeout](https://upstash.com/docs/redis/sdks/ratelimit-ts/features) normally returns `success: true` with `reason: "timeout"`; this recipe explicitly denies that result. Redis request deadlines, no automatic retries, disabled analytics/telemetry and disabled ephemeral blocking cache make the check bounded and observable. The pinned client's `retry: { retries: 0 }` is deliberate. Do not replace it with an unreviewed default or assume every successful SDK response establishes enforcement.

The local request cap defaults to 12; `prepare --max-requests` accepts **1–16** and freezes that budget in the reviewed state. A deliberately smaller budget can test exhaustion. It is not the provider's billing meter: [Lua operations, replicas and transactions](https://upstash.com/docs/redis/overall/billing) add billable commands. The [rate-limit cost table](https://upstash.com/docs/redis/sdks/ratelimit-ts/costs) estimates four commands for an allowed token-bucket call and two for a denied call; script-cache misses can add a request. Analytics would add traffic, so it stays disabled. Local budget exhaustion and provider quota exhaustion both remain visible failures; neither selects another database or upgrades billing.

`requests` and `cleanup_requests` are recorded when their operation finishes. A running or crashed receipt showing zero is not proof that no remote request was dispatched. Use the original task/attempt and provider evidence when investigating uncertainty.

## Recover and clean up explicitly

After a lost task response, inspect the original receipt and exact attempt before deciding anything else. A request timeout stops local authorization and aborts/freezes further local transport; it does not prove that an already-dispatched remote operation failed. Redis counters or cache keys may still have changed. Do not blindly retry a verification or prepare another state while its original task may remain active. A recorded passed run returns its known status without another provider request or client-configuration read. Other started states are never rerun: inspect running, failed or uncertain results and make an explicit decision before preparing a new test identity.

Mutations hold an exclusive `.busy` file in their own state directory. A crash can leave it behind; the recipe never breaks a stale lock automatically. `inspect` remains available. Confirm the original AXV attempt is final and its process has stopped, review its state, then manually remove only that test directory's stale `.busy` file. Removing a lock does not authorize replay of a started verification or establish that remote work was cancelled.

For cleanup, first confirm all original attempts are final and inspect their outcome. Submit `cleanup --confirm-stopped` as a separate reviewed task, using the same executable, state/config paths and approval digest as verification; the flag explicitly attests that the original work stopped. This permits cleanup of a running receipt after the process ended and any stale lock was reviewed as above. Cleanup issues `DEL` only for the two recorded sample keys. It does not scan prefixes, call broad SDK reset APIs, flush the database or run automatically in a `finally` block. A late remote operation may recreate data after local abort or cleanup; the sample TTLs bound its lifetime. Repeated cleanup must still address the same reviewed database and keys. A restored project or replacement VM must not silently adopt an old binding; explicitly review the original remote identity and reconfigure the guest before cleanup or another test.

Cleanup records its own `cleanup_status`, `cleanup_error` and `cleanup_requests`; it preserves the original verification status, error and request count. A cleanup failure does not turn a previously passed verification into a failed check. An uncertain cleanup requires inspection and a separate operator decision; it is not automatically replayed.

```sh
axv vm project task submit <project-id> \
  --binding-file upstash-task-binding.json --request-id upstash-cleanup-001 \
  --timeout 1m --max-log-bytes 4096 -- \
  /usr/local/bin/node /projects/upstash-demo/axv/recipes/upstash/sample.mjs cleanup \
  --state /home/axv/.local/state/axv-upstash-dev/check-001 \
  --config /home/axv/.config/axv-upstash-dev/config.json \
  --approve <approval-digest> --confirm-stopped
```

Detach by stopping application use and removing only this recipe's private local configuration after resolving its tasks. Review remote dependencies before revoking credentials or deleting the database in the Upstash console. Resetting the database password revokes both default REST tokens and affects other clients; do not treat that as isolated recipe-key revocation. Browser close, agent End, VM replacement and local file deletion never delete or reassign the hosted database. Root-disk credentials/state are outside project-volume backups; retain needed non-secret receipts and original remote identities independently.

## Verification scope

Run `pnpm exec vitest run scripts/upstash-recipe.test.mjs` and the [required repository checks](../../docs/development.md#verification). Fixtures must cover TTL expiry, allowed/denied/refill results, successful timeout-shaped responses denied, network/authentication/quota failures, local request-budget exhaustion, state/config identity mismatch, safe output and exact-key cleanup. They do not establish live provider acceptance.

Live acceptance requires a compatible guest and a claimed user-owned development database. Record source/client/runtime versions, region and non-secret database/application identity, original task/attempt receipts, observed TTL/deny results and sanitized evidence. Inspect output for leaks. If the account, claimed database or compatible guest is unavailable, record each unavailable check explicitly; do not substitute fixtures for a live result or create accountless resources automatically.
