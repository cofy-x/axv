# Documentation map

Find the single document that owns the fact you are changing. [AGENTS.md](../AGENTS.md) holds repository-wide rules and [project status](../README.md#project-status) records current availability.

## Choose context

- **Orientation:** Read [README](../README.md), [ownership overview](architecture.md#ownership-and-dependencies) and [lifetime comparison](architecture.md#codex-attachment-and-process-lifetime). Use [product direction](product.md) for identity, scope and open decisions.
- **Feature work:** Start with the ownership overview and one row in [Read by task](#read-by-task). Read only that owner's affected sections and linked implementation files. Use the [API map](api/README.md#find-the-implementation) for routes and [state ownership](architecture.md#state-ownership) for lifecycle changes.
- **Documentation work:** Start with [Content ownership](#content-ownership) and verify facts against the implementation or tests.

Add [cross-cutting context](#cross-cutting-context) only for affected boundaries, run the required [checks](development.md#verification), and follow [Maintain the map](#maintain-the-map).

## Read by task

Choose the row for the behavior being changed. The primary reference owns the mechanism and source links; additional references are conditional.

| Task | Start here | Add when affected |
| --- | --- | --- |
| Applications, windows, menus, workspaces or layout | [Desktop implementation map](desktop.md#implementation-map) | [Desktop state API](api/desktop.md) for serialized state |
| Installed applications, packages, sandbox sessions or private app data | [Installed application API](api/extensions.md) | [Desktop architecture](desktop.md#installed-extensions); [extension storage](storage.md#installed-application-storage) |
| Host project browsing and registration | [Project root access](storage.md#project-root-access) | [Host project files API](api/project-files.md) |
| Host shells or project terminals | [Terminal ownership](desktop.md#terminal-sessions) | [Host Terminal API](api/host-terminal.md) |
| Shared host directory selection | [Host directories API](api/host-directories.md) | The consuming Terminal, Codex or Project Files reference |
| Codex conversations and runs | [Codex implementation map](codex.md#implementation-map) | [Host Codex API](api/host-codex.md) |
| VM project Codex sessions or bound AXV Skill tools | [VM work context](codex.md#vm-project-work-context) | [Bundled Skill](../apps/server/internal/vmwork/skills/axv-vm-project/SKILL.md), [bound tools](startup.md#bound-vm-project-tools-for-codex), [VM project contract](api/compute.md#vm-projects) |
| VMs, volumes, images or guest execution | [VM implementation map](vm-platform.md#implementation-map) | [Compute API](api/compute.md), [Guest API](api/guest.md), [VM CLI](startup.md#vm-command-line) or [image recipes](../images/guest/README.md) |
| VM projects, retained-volume bindings or guest project files | [VM projects](vm-platform.md#vm-projects) | [Compute project API](api/compute.md#vm-projects), [guest file enforcement](api/guest.md#project-file-protocol), [VM project CLI](startup.md#vm-project-files) |
| VM project creation, Git cloning or initialization recovery | [Project initialization](vm-platform.md#project-initialization) | [Compute initialization API](api/compute.md#vm-project-initialization), [VM project CLI](startup.md#vm-project-initialization) |
| VM project GitHub association, PRs or commit checks | [GitHub project review](vm-platform.md#github-project-review) | [Compute GitHub contract](api/compute.md#github-project-review), [guest read protocol](api/guest.md#github-project-reads), [usage](usage.md#github-project-review) |
| Automatic GitHub PR discovery, Codex review and local candidate comments | [GitHub Review API](api/github-review.md) | [Dedicated review VM recipe](../recipes/github-review/README.md), [usage](usage.md#github-review), [lifecycle](startup.md#daily-commands) |
| Offline project backups, restoration or explicit copy binding | [Project backups](vm-platform.md#project-volume-backups) | [Compute backup API](api/compute.md#vm-project-backups), [guest file enforcement](api/guest.md#project-file-protocol) |
| VM development-service previews or their remote controls | [Development previews](api/project-previews.md) | [VM architecture](vm-platform.md#development-previews), [access boundaries](api/README.md#access-and-identity-boundaries) |
| Persistent VM project tasks, logs, cancellation or recovery | [Task ownership](vm-platform.md#persistent-project-tasks) | [Compute task API](api/compute.md#vm-project-tasks), [guest execution](api/guest.md#project-task-protocol), [task CLI](startup.md#vm-project-tasks), [guest credentials](usage.md#credentials-for-project-tasks) |
| Optional development-application email or Redis recipes | [Resend recipe](../recipes/resend/README.md) or [Upstash recipe](../recipes/upstash/README.md) | [Guest credentials](usage.md#credentials-for-project-tasks), [task CLI](startup.md#vm-project-tasks) |
| Installation, uninstallation, configuration, startup or supervision | [Startup](startup.md) | [Runtime identity API](api/desktop.md#runtime-health-and-bootstrap) |
| Work notifications, event intake, browser push or Pocket Alert | [Notifications API](api/notifications.md) | [Codex producer](codex.md#provider-activity), [task producer](api/compute.md#vm-project-tasks), [usage](usage.md#work-notifications) |
| Private remote access (Tailscale) or Cloudflare Access / publishing | [Remote access setup](remote-access.md) | [Tailscale API](api/tailscale.md), [Cloudflare API](api/cloudflare.md), [access boundaries](api/README.md#access-and-identity-boundaries) |
| Shared editors, rendering, filesystem primitives, diagnostics or downloads | [Shared infrastructure](architecture.md#shared-infrastructure) | The consuming feature reference |
| Build tooling, automated tests, CI or CodeQL | [Development](development.md) | [Acceptance](acceptance.md) for physical-device, native-VM or live-provider checks |
| Release preparation, provenance or third-party notices | [Releasing](releasing.md) | [Third-party notices](../THIRD_PARTY_NOTICES.md), [acceptance](acceptance.md#linux-firecracker), [image recipes](../images/guest/README.md) |
| Product decisions or capability claims | [Product direction](product.md) and [project status](../README.md#project-status) | The affected usage or implementation reference |
| User actions and session behavior | [Using AXV](usage.md) | [Startup](startup.md) for installation and CLI workflows |

Contribution, support, conduct and vulnerability-reporting paths are linked from [README](../README.md#contributing-and-support).

## Cross-cutting context

Apply these reads only to affected boundaries:

- **UI changes:** Relevant sections of [Design](design.md) (`settings-and-disclosures`, `interface-copy`, `acceptance-checks`).
- **Startup, lifecycle or config changes:** [Startup](startup.md).
- **HTTP, WebSocket or guest protocol changes:** [API index](api/README.md) shared rules and the affected API page.
- **Product boundary changes:** [Product direction](product.md).

## Content ownership

Each fact has one owner. Other pages may state the consequence their reader needs and link to the owner for full rules or procedures.

| Owner | Keep here |
| --- | --- |
| [Project README](../README.md) | Product overview, platform/validation status, pinned toolchain summary and quickstart |
| [AGENTS.md](../AGENTS.md) | Repository layout, working rules and high-risk guardrails |
| [This map](README.md) | Reading paths, content ownership and documentation maintenance |
| [Product direction](product.md) | Identity, established direction, boundaries and open decisions |
| [Architecture overview](architecture.md) | Component dependencies, identity boundaries, cross-service lifetimes and shared infrastructure |
| [Desktop architecture](desktop.md) | Application registration, lazy loading, mounted windows, menus, layout persistence and Host Terminal |
| [Desktop storage](storage.md) | Desktop/extension database baselines, migration runner, browser recovery identity and host project roots |
| [Host Codex architecture](codex.md) | Provider integration, process/run ownership, VM work context, reconciliation and activity journals |
| [VM architecture](vm-platform.md) | Host/guest ownership, preparation, durable compute state, providers, VM projects and persistent tasks |
| [API reference](api/README.md) | Routes, fields, protocol limits, errors, access checks and caller retry obligations |
| [Startup](startup.md) | Installation, uninstallation, daily/VM CLI workflows, configuration precedence, paths, cache and Linux/macOS VM setup |
| [Remote access](remote-access.md) | Tailscale and Cloudflare Access setup, tunnel/publishing configuration and CLI commands |
| [Usage](usage.md) | Visible UI actions, confirmations and user-facing outcomes |
| [Design](design.md) | Visual language, interaction, focus, copy and narrow-screen accessibility |
| [Development](development.md) | Local dev, production builds, bundle budgets, verification matrix, CI, CodeQL, dependency patches and test isolation |
| [Acceptance](acceptance.md) | Manual physical-device, browser-install, Web Push, live-provider and native-VM acceptance runbooks |
| [Releasing](releasing.md) | Version selection, multi-platform/Guest candidate builds, provenance verification and publication |
| [Image recipes](../images/guest/README.md) | Guest image-building inputs, prerequisites, package sources and commands |
| [Bundled VM Skill](../apps/server/internal/vmwork/skills/axv-vm-project/SKILL.md) | Runtime agent prompt and bound CLI contract injected into VM-project Codex sessions |
| [Recipes](../recipes/github-review/README.md) | Dedicated GitHub Review VM ([GitHub Review](../recipes/github-review/README.md)), optional dev email ([Resend](../recipes/resend/README.md)) and Redis ([Upstash](../recipes/upstash/README.md)) |
| [Third-party notices](../THIRD_PARTY_NOTICES.md) | Distribution boundaries, upstream sources and retained license notices |
| [Community docs](../CONTRIBUTING.md) | [Contributing](../CONTRIBUTING.md), [Support](../SUPPORT.md), [Code of Conduct](../CODE_OF_CONDUCT.md), [Security](../SECURITY.md) |

Manifests and lockfiles declare toolchain versions; code and tests establish current behavior. When code disagrees with an intentional rule or product decision, report and resolve the discrepancy explicitly rather than silently rewriting the rule.

## Maintain the map

- **One owner, replace in place:** Verify claims against implementation or tests, update the single owner first and link from other pages. When behavior changes, rewrite or delete obsolete text in place — never append historical caveats (`previously`, `legacy`, `in this phase`).
- **Keep docs thin (invariants over code mirrors):** Document architecture boundaries, invariants, ownership, user/operator contracts and source entry points. Do not mirror Go structs, exhaustive constant lists or UI button copy that code and tests already enforce.
- **Readable size and shape:** Aim to keep individual architecture and API pages concise (~150–250 lines). Prefer tables and short bullet lists over long prose paragraphs.
- **No permanent audit/roadmap pages:** Keep task plans, PR validation logs and screenshots in task/PR history.
- **Verify links and format:** Preserve code-referenced anchors or update all inbound links when moving a section, then run `make format-check check-docs` and the [documentation review](development.md#documentation-review).
