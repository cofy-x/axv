# AXV product direction

This document owns identity, goals and product decisions. The [README](../README.md#project-status) owns availability; the [documentation map](README.md) routes implementation and usage questions. Goals below are not claims of implemented functionality.

## Identity

| Item | Convention |
| --- | --- |
| Product name | **AXV** in prose and the interface |
| Repository namespace | `cofy-x/axv` |
| Executable and project slug | `axv` |
| npm namespace | `@cofy-x/axv` and its workspace packages |
| Go module base | `github.com/cofy-x/axv` |
| Package description | A personal cloud workstation. |
| Tagline | **Your personal cloud for VMs and coding agents.** |
| Name interpretation | **Agents × Virtualization** |

**A** represents agents, **V** represents virtualization, and **X** connects them, with exploration, connection and possibility as brand themes. This interpretation need not be expanded everywhere. Internal Nextstep names describe the desktop's design heritage and do not need a branding migration.

## Established direction

- Build a self-contained personal cloud workstation deployed on a person's Mac or Linux machine. VM management and multiple host coding agents are core capabilities in the long-term direction; Codex is the current implementation focus, and Claude Code remains a core integration goal.
- Preserve the NEXTSTEP / Window Maker desktop foundation while making content comfortable for reading and coding. [Design](design.md) owns the interaction and visual requirements.
- Make a progressive web app (PWA) the primary client, with the deployment machine owning services, running work and storage. Returning from another device should recover access to that work. PWA delivery is a product goal, not a current availability claim.
- Treat access from different devices and native VM hosting on different platforms as separate deliverables. Browser support does not imply hypervisor support.
- Manage agents and VMs on the machine where AXV is deployed. Setup follows that machine's operating system; users select projects, sessions and VMs within this deployment. Host selection and scheduling across multiple machines are outside this direction.
- Make the installed `axv` command the normal workstation entry, composing independent desktop and compute services under one profile. Startup and updates must preserve persistent work; development profiles remain separate from daily use. [Startup](startup.md) owns commands, configuration and service operation.
- Support local VM workflows through both the desktop and `axv vm`, sharing host state and operation rules. Standard SSH, file transfer and forwarding remain local capabilities, independent of a future remote gateway or agent-to-VM tools. [VM CLI workflows](startup.md#vm-command-line) own command behavior.
- Make the desktop useful before VM preparation finishes. Preparation belongs inside Virtual Machines, requires review before installation or downloads, and makes a verified service available without restarting the interface. [Usage](usage.md#virtual-machines) owns the current workflow.
- Serve one person's workstation, with local access first and Tailscale as the planned private remote-access path. Remote identity, authorization and transport boundaries still need design; network connectivity alone must not bypass local protections.

## Agent and VM responsibilities

Managed coding agents run on the AXV deployment machine as the long-term model. Prefer official provider tools and preserve their authentication, settings and session capabilities. For Codex, use the official CLI so users retain its project trust, approvals, sandbox and model settings. AXV supplies integration and session access; it does not replace the provider's agent loop or authentication interface. Installing and supervising coding agents inside VMs is outside the current plan.

VMs provide the development environment for VM projects: project code, Git worktrees, dependencies, builds, tests and development services. The existing guest service remains the execution foundation; it is not a coding-agent runtime. [VM architecture](vm-platform.md#guest-execution) owns its implemented behavior.

For a VM workflow, the authoritative project code and Git worktrees belong on its persistent project volume. The host agent operates on that copy through AXV tools. Existing host projects remain independent workflows; AXV does not automatically migrate, mount or synchronize them with VM projects.

The intended agent-to-VM integration provides stable operations for commands, project files, task status and development services, with an AXV Skill guiding host agents in their use. These are capability goals; tool interfaces and protocols remain undecided. A Skill does not redirect an agent's built-in host tools or change its permissions. [Guest API](api/guest.md) documents the current execution interface and its limits.

Conversations outlive views. Closing a view, ending a process and continuing saved context are separate user choices. Failed recovery must be visible rather than silently starting unrelated work. Naming and archiving organize conversations without changing process lifetime. [Codex architecture](codex.md) owns run identity and recovery mechanisms; [usage](usage.md#codex) owns the corresponding actions.

## Intended workflow

The target workflow is:

1. Choose a project and its location: an existing host directory or a VM's persistent project volume.
2. Start or resume a host agent session with that project context explicit.
3. For a VM project, use AXV tools to read and change its files, run commands and follow tasks inside the selected VM. Host projects continue to use their host context.
4. Review code, test results and development services alongside the session and the corresponding terminal.

This complete workflow is a product goal; [project status](../README.md#project-status) records what is available. Every action should identify its project and execution location, independently of where the agent process runs.

Desktop users can arrange multiple windows. On phones, prioritize choosing a machine, entering a session, reading progress and responding to approvals. A dedicated narrow-screen workflow remains a goal; do not mistake responsive desktop fitting for that complete workflow.

| Area | Intended responsibility |
| --- | --- |
| Virtual Machines | VM inventory, lifecycle, resources and connection state |
| Agents | Host coding-agent sessions, progress, approvals and recovery |
| Project files | The authoritative host or VM project copy, addressed by its location |
| Terminal | Interactive access to an explicit host or VM project context |
| Inspector | Context for the selected machine, project or session |

Use **Virtual Machines** for the application/window title, **VMs** for compact labels and **New VM** for creation. Future application layouts can evolve; do not add nonfunctional launchers to represent the roadmap.

## Product boundaries

- **Execution context is explicit.** Agent process location, project location and command execution location are distinct. An agent's host working directory does not select the VM or directory for a guest operation. Commands and file operations must address the intended project; a missing VM must not silently redirect work to the host.
- **Organization is not execution.** Desktop workspaces arrange windows; they neither select VMs nor determine machine/session counts. Placing an agent window beside a VM does not move the agent into it.
- **Virtual documents are not project files.** Files and Text Editor serve a separate document store. Project access must address the chosen host directory or VM project volume, including when a host agent operates on a VM project.
- **Documents carry explicit work context.** Documents hold task briefs, environment notes, runbooks and reports. Links identify related projects, VMs and conversations without selecting an execution target. Cross-store exchange is an explicit snapshot copy. Attaching context selects saved content and a particular run; later edits and continuation do not silently replace or replay that attachment.
- **Persistence is a deliberate choice.** VMs, project volumes, agent conversations, process runs and windows retain separate lifetimes. Managed agent work can survive a detached view; ordinary host shells are ephemeral. New clients and integrations must preserve the existing [lifecycle contracts](architecture.md#codex-attachment-and-process-lifetime).
- **Availability is evidence-based.** Implemented adapters, automated fixture coverage and native platform validation are different claims. Record current support in the README; keep validation commands in [development](development.md).

Official versioned AXV enhanced images are the intended long-term default preparation source; changing the default requires an explicit product decision. Advanced users may explicitly choose custom images, with AXV guest software supplying optional enhanced features. [VM architecture](vm-platform.md#image-and-capability-contract) owns the image and capability contract, [startup](startup.md#first-time-macos-setup) owns preparation choices, and the [README](../README.md#project-status) owns publication and validation status.

## Decisions still open

- The tool interface and Skill integration for VM commands and project-file operations, including Git/worktree management on the project volume.
- Long-running task identity, status, logs, cancellation and recovery; automatic session supervision after host boot and recovery after VM replacement.
- Development-service discovery, access and preview.
- Provider-specific session integration, including Claude Code and structured agent interfaces.
- PWA delivery and reconnection behavior, plus remote identity, authorization and transport when introducing Tailscale.

Resolve these in focused implementation tasks. Record settled rationale here; change architecture and API references when the behavior exists. Keep task plans and review logs in task/PR history rather than adding a parallel roadmap or audit document.
