# Documentation map

Find the document that owns the fact you are changing. [AGENTS.md](../AGENTS.md) holds repository-wide rules and [project status](../README.md#project-status) records current availability.

## Choose context

- **Orientation** starts with the [README](../README.md), [ownership overview](architecture.md#ownership-and-dependencies) and [lifetime comparison](architecture.md#codex-attachment-and-process-lifetime). Use [product direction](product.md) for scope and goals.
- **Feature work** starts with the ownership overview and one row in [Read by task](#read-by-task). Read that owner's affected sections and implementation links. Trace the caller, state, execution and tests that the change touches. Use the [API map](api/README.md#find-the-implementation) for route work and [state ownership](architecture.md#state-ownership) for lifecycle changes.
- **Documentation work** starts with [Content ownership](#content-ownership) and the implementation or tests that establish the facts.

Add [cross-cutting context](#cross-cutting-context) for each affected boundary. Choose the required [checks](development.md#verification) and follow [Maintain the map](#maintain-the-map) for affected docs.

## Read by task

Choose the row for the behavior being changed. The primary reference owns the mechanism and source links; additional references are conditional.

| Task | Start here | Add when affected |
| --- | --- | --- |
| Applications, windows, menus, workspaces or layout | [Desktop implementation map](desktop.md#implementation-map) | [Desktop state API](api/desktop.md) for serialized state |
| Installed applications, packages, sandbox sessions or private app data | [Installed application API](api/extensions.md) | [Desktop architecture](desktop.md) for catalog and mounted windows; [extension storage](storage.md#installed-application-storage) for database ownership |
| Host project browsing and registration | [Project root access](storage.md#project-root-access) | [Host project files API](api/project-files.md) |
| Host shells or project terminals | [Terminal ownership](desktop.md#terminal-sessions) | [Host Terminal API](api/host-terminal.md) |
| Shared host directory selection | [Host directories API](api/host-directories.md) | The consuming Terminal, Codex or Project Files reference |
| Codex conversations and runs | [Codex implementation map](codex.md#implementation-map) | [Host Codex API](api/host-codex.md) |
| VM project Codex sessions or bound AXV Skill tools | [VM work context](codex.md#vm-project-work-context) | [Bound tools](startup.md#bound-vm-project-tools-for-codex), [VM project contract](api/compute.md#vm-projects) and [project task contract](api/compute.md#vm-project-tasks) |
| VMs, volumes, images or guest execution | [VM implementation map](vm-platform.md#implementation-map) | [Compute API](api/compute.md), [Guest API](api/guest.md), [VM CLI](startup.md#vm-command-line) or [image recipes](../images/guest/README.md), according to the changed boundary |
| VM projects, retained-volume bindings or guest project files | [VM projects](vm-platform.md#vm-projects) | [Compute project API](api/compute.md#vm-projects), [guest file enforcement](api/guest.md#project-file-protocol), [VM project CLI](startup.md#vm-project-files); [product constraints](product.md#vm-project-workspace-constraints) for scope |
| VM project creation, Git cloning or initialization recovery | [Project initialization](vm-platform.md#project-initialization) | [Compute initialization API](api/compute.md#vm-project-initialization), [VM project CLI](startup.md#vm-project-files) |
| VM project GitHub association, PRs or commit checks | [GitHub project review](vm-platform.md#github-project-review) | [Compute GitHub contract](api/compute.md#github-project-review), [guest read protocol](api/guest.md#github-project-reads), [usage](usage.md#github-project-review) |
| Automatic GitHub PR discovery, Codex review and local candidate comments | [GitHub Review](api/github-review.md) | [Application setup](usage.md#github-review), [lifecycle](startup.md#daily-commands) |
| Offline project backups, restoration or explicit copy binding | [Project backups](vm-platform.md#project-volume-backups) | [Compute backup API](api/compute.md#vm-project-backups), [guest file enforcement](api/guest.md#project-file-protocol) |
| VM development-service previews or their remote access | [Development previews](api/project-previews.md) | [VM architecture](vm-platform.md#development-previews), [access boundaries](api/README.md#access-and-identity-boundaries) |
| Persistent VM project tasks, logs, cancellation or recovery | [Task ownership](vm-platform.md#persistent-project-tasks) | [Compute task API](api/compute.md#vm-project-tasks), [guest execution](api/guest.md#project-task-protocol), [task CLI](startup.md#vm-project-tasks), [guest-owned credentials](usage.md#credentials-for-project-tasks), [native acceptance](development.md#vm-project-workspace-acceptance) |
| Optional development-application email recipe | [Resend recipe](../recipes/resend/README.md) | [Guest-owned credentials](usage.md#credentials-for-project-tasks), [task CLI](startup.md#vm-project-tasks), [task acceptance](api/compute.md#vm-project-tasks) |
| Optional development-application cache or rate-limit recipe | [Upstash recipe](../recipes/upstash/README.md) | [Guest-owned credentials](usage.md#credentials-for-project-tasks), [task CLI](startup.md#vm-project-tasks), [task acceptance](api/compute.md#vm-project-tasks) |
| Installation, uninstallation, configuration, startup or supervision | [Startup](startup.md) | [Runtime identity API](api/desktop.md) for identity contracts |
| Work notifications, event intake, browser push or Pocket Alert delivery | [Notifications API and ownership](api/notifications.md) | [Codex producer](codex.md#provider-activity), [task producer](api/compute.md#vm-project-tasks), [notification usage](usage.md#work-notifications), [push acceptance](development.md#web-push-acceptance) |
| Private remote access, Tailscale enrollment or authorization | [Tailscale API and ownership](api/tailscale.md) | [Remote setup](startup.md#remote-access) for configuration; [shared access boundaries](api/README.md#access-and-identity-boundaries) |
| Cloudflare desktop access, account setup or service publishing | [Cloudflare API and ownership](api/cloudflare.md) | [Cloudflare setup](startup.md#cloudflare-access) for deployment; [project previews](api/project-previews.md) for the separate local workflow; [shared access boundaries](api/README.md#access-and-identity-boundaries) |
| Shared editors, rendering, filesystem primitives, diagnostics or downloads | [Shared infrastructure](architecture.md#shared-infrastructure) | The consuming feature reference for target, state and lifetime ownership |
| Build tooling, tests or acceptance | [Development](development.md) | The affected feature reference |
| Release preparation, distribution or third-party notices | [Releasing](releasing.md) | [Third-party notices](../THIRD_PARTY_NOTICES.md) for distribution boundaries and upstream sources; [Development](development.md) and [image recipes](../images/guest/README.md) for required acceptance |
| Product decisions or capability claims | [Product direction](product.md) and [project status](../README.md#project-status) | The affected usage or implementation reference |
| User actions and session behavior | [Using AXV](usage.md) | [Startup](startup.md) for installation and CLI workflows |

Contribution, support, conduct and vulnerability-reporting paths are linked from the [project README](../README.md#contributing-and-support).

## Cross-cutting context

Apply these reads only to affected boundaries:

- For UI changes, read the relevant [design rules](design.md), including [settings and disclosures](design.md#settings-and-disclosures), [interface copy](design.md#interface-copy) and [acceptance checks](design.md#acceptance-checks).
- Service startup/lifecycle, deployment, environment or configuration changes: [Startup](startup.md).
- HTTP, WebSocket or guest protocol changes: shared rules in the [API index](api/README.md) and the affected contract.
- Product boundary changes: [Product direction](product.md). Existing implementation work does not require rereading unrelated goals.

## Content ownership

Each fact has one owner. Another page may state the consequence its reader needs, then link to the owner for the mechanism, complete contract or procedure.

| Owner | Keep here |
| --- | --- |
| [Project README](../README.md) | Product overview, current capability and native-validation status, pinned toolchain summary and quickstart |
| [AGENTS.md](../AGENTS.md) | Repository-wide working rules and brief high-risk guardrails |
| [Contributing](../CONTRIBUTING.md) | AXV contribution workflow and links to organization expectations |
| [Support](../SUPPORT.md) | Public support channels, report context and diagnostic redaction |
| [Code of Conduct](../CODE_OF_CONDUCT.md) | Organization conduct rules and AXV's incident-reporting channels |
| [Security policy](../SECURITY.md) | Security support scope, private vulnerability reporting and disclosure process |
| [This map](README.md) | Reading paths, content ownership and documentation maintenance |
| [Product direction](product.md) | Identity, rationale, settled direction and open product decisions |
| [Architecture overview](architecture.md) | Component dependencies, identity boundaries and cross-service lifetimes |
| [Desktop architecture](desktop.md) | Application registration, mounted windows, menu ownership, layout persistence and Host Terminal ownership |
| [Desktop storage](storage.md) | Desktop database baseline, browser recovery identity and registered host projects |
| [Host Codex architecture](codex.md) | Provider integration, process/run ownership, reconciliation and delivery persistence |
| [VM architecture](vm-platform.md) | Host/guest ownership, durable compute state, preparation and provider mechanisms |
| [API reference](api/README.md) | Routes, fields, protocol limits, errors, access checks and caller retry/lifecycle obligations |
| [Startup](startup.md) | Installation, uninstallation, CLI workflows, configuration precedence, paths, environment and service operation |
| [Usage](usage.md) | Visible user actions, confirmations and their effects |
| [Resend recipe](../recipes/resend/README.md) | Optional development sender setup, guest-private credentials, reviewed sends and provider-side uncertainty |
| [Upstash recipe](../recipes/upstash/README.md) | Optional development Redis setup, guest-private credentials, bounded cache/rate-limit checks and remote cleanup |
| [Development](development.md) | Build/verification commands, CI and security analysis, prerequisites, test isolation and diagnostics |
| [Releasing](releasing.md) | Version selection, candidate review, promotion and artifact publication |
| [Third-party notices](../THIRD_PARTY_NOTICES.md) | Distribution boundaries, upstream sources and retained license notices |
| [Design](design.md) | Visual language, interaction, focus and accessibility |
| [Image recipes](../images/guest/README.md) | Image-building inputs, prerequisites and commands |

Manifests and lockfiles declare toolchain versions; the README summarizes them. Code and tests establish current behavior. Rules and product decisions constrain intended changes: if implementation disagrees, identify and resolve the discrepancy explicitly rather than silently rewriting a rule to match it.

## Maintain the map

- Verify claims against their implementation or tests and update the owner first. Use brief links from other pages. Keep user consequences where the workflow needs them.
- Keep repeatable UI rules in [Design](design.md) and action paths and outcomes in [Usage](usage.md). Put implementation in the feature owner and exact values in the runbook, API or manifest that needs them. Keep README, Welcome, About and metadata consistent when capabilities change.
- Extend an existing owner before adding a page. Split only for a distinct responsibility or reading task. Keep `AGENTS.md` short and reserve scoped rules for directory-specific requirements.
- Link to stable source entry points and tests. Prefer descriptive headings, relative links, tables for comparisons and Mermaid for relationships. Keep examples current.
- Use Releases for downloads and select the intended application or Guest asset from the shared catalog. Keep exact tags and URLs in reproducible procedures and release evidence. Link provider pricing, quotas and eligibility to their authorities.
- Keep plans, audits and dated validation in task or PR history. For major desktop changes, attach sanitized visual evidence to the PR. A representative scene with Welcome and Codex should leave room for icon labels. Use demo-scoped settings for provider notices and banners. Keep screenshots outside commits.
- Preserve anchors or update inbound links when moving a section, then run the [documentation checks](development.md#documentation-review).
