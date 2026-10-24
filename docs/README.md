# Documentation map

Use this page to choose context and find the owner of a fact. [AGENTS.md](../AGENTS.md) owns repository-wide working rules; [project status](../README.md#project-status) records current availability. Read the relevant sections and follow their source links rather than loading every reference.

## Read by task

Before application changes, read [ownership and dependencies](architecture.md#ownership-and-dependencies), then start with one row below. Follow another reference only when the change affects its boundary. Documentation-only changes start with the existing content owner and its implementation links.

| Task | Start here | Add when affected |
| --- | --- | --- |
| Applications, windows, menus, workspaces or layout | [Desktop architecture](desktop.md) | [Desktop state API](api/desktop.md) for serialized state |
| Installed applications, packages, sandbox sessions or private app data | [Installed application API](api/extensions.md) | [Desktop architecture](desktop.md) for catalog and mounted windows; [extension storage](storage.md#installed-application-storage) for database ownership |
| Virtual documents, saves, revisions or recovery | [Document storage](storage.md) | [Files API](api/files.md); [database identity](api/desktop.md#database-identity-and-local-recovery) for browser recovery |
| Project browsing, links, snapshots or copies | [Storage ownership](storage.md#references-snapshots-and-project-exchange) | [Project files and exchange API](api/project-files.md) |
| Host shells or project terminals | [Terminal ownership](desktop.md#terminal-sessions) | [Host Terminal API](api/host-terminal.md) |
| Shared host directory selection | [Host directories API](api/host-directories.md) | The consuming Terminal, Codex or Project Files reference |
| Codex conversations, runs or document delivery | [Host Codex architecture](codex.md) | [Host Codex API](api/host-codex.md) |
| VM project Codex sessions or bound AXV Skill tools | [VM work context](codex.md#vm-project-work-context) | [Bound tools](startup.md#bound-vm-project-tools-for-codex), [VM project contract](api/compute.md#vm-projects) and [project task contract](api/compute.md#vm-project-tasks) |
| VMs, volumes, images or guest execution | [VM architecture](vm-platform.md) | [Compute API](api/compute.md), [Guest API](api/guest.md), [VM CLI](startup.md#vm-command-line) or [image recipes](../images/guest/README.md), according to the changed boundary |
| VM projects, retained-volume bindings or guest project files | [VM projects](vm-platform.md#vm-projects) | [Compute project API](api/compute.md#vm-projects), [guest file enforcement](api/guest.md#project-file-protocol), [VM project CLI](startup.md#vm-project-files); [product constraints](product.md#vm-project-workspace-constraints) for scope |
| VM project creation, Git cloning or initialization recovery | [Project initialization](vm-platform.md#project-initialization) | [Compute initialization API](api/compute.md#vm-project-initialization), [VM project CLI](startup.md#vm-project-files) |
| Offline project backups, restoration or explicit copy binding | [Project backups](vm-platform.md#project-volume-backups) | [Compute backup API](api/compute.md#vm-project-backups), [guest file enforcement](api/guest.md#project-file-protocol) |
| VM development-service previews or their remote access | [Development previews](api/project-previews.md) | [VM architecture](vm-platform.md#development-previews), [access boundaries](api/README.md#access-and-identity-boundaries) |
| Persistent VM project tasks, logs, cancellation or recovery | [Task ownership](vm-platform.md#persistent-project-tasks) | [Compute task API](api/compute.md#vm-project-tasks), [guest execution](api/guest.md#project-task-protocol), [task CLI](startup.md#vm-project-tasks), [native acceptance](development.md#vm-project-workspace-acceptance) |
| Installation, uninstallation, configuration, startup or supervision | [Startup](startup.md) | [Runtime identity API](api/desktop.md) for identity contracts |
| Private remote access, Tailscale enrollment or authorization | [Remote setup](startup.md#remote-access) | [Tailscale remote access API](api/tailscale.md); [shared access boundaries](api/README.md#access-and-identity-boundaries) |
| Cloudflare desktop access, account setup or service publishing | [Cloudflare setup](startup.md#cloudflare-access) | [Cloudflare API](api/cloudflare.md); [project previews](api/project-previews.md) for the separate local workflow; [shared access boundaries](api/README.md#access-and-identity-boundaries) |
| Build tooling, tests or acceptance | [Development](development.md) | The affected feature reference |
| Contributions, bug reports or usage questions | [Contributing](../CONTRIBUTING.md) and [Support](../SUPPORT.md) | [Development](development.md) for verification |
| Community conduct or sensitive incidents | [Code of Conduct](../CODE_OF_CONDUCT.md) | [Support](../SUPPORT.md) for public channels |
| Reporting a suspected vulnerability | [Security policy](../SECURITY.md) | [API access boundaries](api/README.md#access-and-identity-boundaries) for the affected contract |
| Release preparation, distribution or third-party notices | [Releasing](releasing.md) | [Development](development.md) and [image recipes](../images/guest/README.md) for required acceptance |
| Product decisions or capability claims | [Product direction](product.md) and [project status](../README.md#project-status) | The affected usage or implementation reference |
| User actions and session behavior | [Using AXV](usage.md) | [Startup](startup.md) for installation and CLI workflows |

Apply these additional reads across task rows:

- UI and interaction changes: [Design](design.md), including [acceptance checks](design.md#acceptance-checks).
- Service startup/lifecycle, deployment, environment or configuration changes: [Startup](startup.md).
- HTTP, WebSocket or guest protocol changes: shared rules in the [API index](api/README.md) and the affected contract.
- Product boundary changes: [Product direction](product.md). Existing implementation work does not require rereading unrelated goals.

## Content ownership

Each fact has one owner. Another page may state the consequence its reader needs, then link to the owner for the mechanism, complete contract or procedure.

| Owner | Keep here |
| --- | --- |
| [Project README](../README.md) | Current capability and native-validation status, pinned toolchain summary and quickstart |
| [AGENTS.md](../AGENTS.md) | Repository-wide working rules and brief high-risk guardrails |
| [Contributing](../CONTRIBUTING.md) | AXV contribution workflow and links to organization expectations |
| [Support](../SUPPORT.md) | Public support channels, report context and diagnostic redaction |
| [Code of Conduct](../CODE_OF_CONDUCT.md) | Organization conduct rules and AXV's incident-reporting channels |
| [Security policy](../SECURITY.md) | Security support scope, private vulnerability reporting and disclosure process |
| [This map](README.md) | Reading paths, content ownership and documentation maintenance |
| [Product direction](product.md) | Identity, rationale, settled direction and open product decisions |
| [Architecture overview](architecture.md) | Component dependencies, identity boundaries and cross-service lifetimes |
| [Desktop architecture](desktop.md) | Application registration, mounted windows, menu ownership, layout persistence and Host Terminal ownership |
| [Document storage](storage.md) | Saves, draft recovery, snapshot/exchange mechanisms, database baseline and transactional initialization |
| [Host Codex architecture](codex.md) | Provider integration, process/run ownership, reconciliation and delivery persistence |
| [VM architecture](vm-platform.md) | Host/guest ownership, durable compute state, preparation and provider mechanisms |
| [API reference](api/README.md) | Routes, fields, protocol limits, errors, access checks and caller retry/lifecycle obligations |
| [Startup](startup.md) | Installation, uninstallation, CLI workflows, configuration precedence, paths, environment and service operation |
| [Usage](usage.md) | Visible user actions, confirmations and their effects |
| [Development](development.md) | Build/verification commands, prerequisites, test isolation and diagnostics |
| [Releasing](releasing.md) | Release preparation, artifact publication and third-party distribution notices |
| [Design](design.md) | Visual language, interaction, focus and accessibility |
| [Image recipes](../images/guest/README.md) | Image-building inputs, prerequisites and commands |

Manifests and lockfiles declare toolchain versions; the README summarizes them. Code and tests establish current behavior. Rules and product decisions constrain intended changes: if implementation disagrees, identify and resolve the discrepancy explicitly rather than silently rewriting a rule to match it.

## Maintain the map

For each change:

1. Find the owner above and verify changed claims against handlers, models, configuration or tests. Update that owner first; remove or shorten duplicate explanations in callers and link to the owner. Keep useful cross-audience consequences.
2. Update affected dependents. Capability changes also need the README, Welcome, About and page metadata aligned. Contract changes need the API page; visible behavior needs usage/design; configuration and CLI changes need startup. Do not touch unrelated pages merely to repeat the update.
3. Review relative links and anchors, then run the [documentation checks](development.md#verification). Preserve useful anchors or update all inbound links when moving a section.

Keep the structure small:

- Extend an existing owner before adding a page. Split for a distinct responsibility or reading task, then update this map and any affected API navigation. Do not add parallel rule files or summaries that track the same facts.
- Keep `AGENTS.md` short and applicable across the repository. Put feature mechanisms in their owner and link them from the task map; add scoped rules only for a real directory-specific requirement.
- Link to a few stable implementation entry points and nearby tests. Avoid exhaustive file inventories, line-number links and copied source declarations. Keep exact values where callers/operators need them, with a source link for review.
- Use descriptive headings, relative links, tables for comparable values and Mermaid for relationships. Examples must match current interfaces.
- Keep plans, audit findings, historical counts and validation diaries in task/PR history. References describe current behavior; product owns intent, and the README owns status. Seeded samples are user documents, not release notes.
