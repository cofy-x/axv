# AXV security policy

This policy supplies AXV's support scope and private reporting address in addition to the [cofy-x security policy](https://github.com/cofy-x/.github/blob/main/SECURITY.md).

## Supported versions

AXV has no supported stable release lines yet. Security fixes target the current default branch; development snapshots and experimental prereleases are not maintained as separate security branches. [Project status](README.md#project-status) records published availability.

## Report a vulnerability

Use [Report a vulnerability](https://github.com/cofy-x/axv/security/advisories/new) in this repository to disclose a suspected vulnerability privately. Do not put exploit details or sensitive material in public issues, pull requests or logs.

Include the affected commit, host OS and architecture, affected component, reproduction steps or a minimal proof of concept, expected access boundary, impact and any known mitigation. Use synthetic files, projects and credentials in reproductions. Share only the details needed to understand the issue; do not send live credentials, private provider history or an entire AXV profile.

Maintainers will assess the report, discuss any additional evidence privately and coordinate a fix and disclosure with the reporter. Response and remediation times depend on the report and maintainer availability; no fixed response schedule is promised.

## Current access boundaries

AXV operates as the host account that launches it. Host terminals and agent sessions can access that account's resources; provider tools retain their own approval and sandbox policies. See [execution identity](docs/startup.md#execution-identity) and the [API access boundaries](docs/api/README.md#access-and-identity-boundaries).

Use the documented local access model or explicitly configured remote entrypoints. Optional Tailscale and Cloudflare desktop entrypoints authenticate and authorize the configured owner; a reverse proxy or tunnel alone does not add an AXV authorization layer. Follow the [remote setup](docs/startup.md#remote-access), [Cloudflare setup](docs/startup.md#cloudflare-access) and [API access boundaries](docs/api/README.md#access-and-identity-boundaries). [Project status](README.md#project-status) owns current capability and validation claims.
