# AXV visual and interaction design

This guide owns recurring visual, interaction, focus, copy and narrow-screen rules. [Desktop architecture](desktop.md) owns implementation; [usage](usage.md) owns user workflows; [product direction](product.md#identity) owns branding.

## Visual system and sources of truth

AXV draws visual inspiration from Window Maker (chrome, Dock, workspaces), GWorkspace (navigation, Inspector) and GNUstep GUI (compact raised/recessed controls) while prioritizing readability, Unicode, keyboard access, touch and modern display scales. Reuse shared controls and tokens; keep feature styles local and demonstrate shared primitives in Component Gallery.

| Concern | Source |
| --- | --- |
| Tokens, chrome and responsive styles | [`packages/nextstep/src/styles.css`](../packages/nextstep/src/styles.css) |
| Shared controls and generic icons | [`packages/nextstep/src/controls.tsx`](../packages/nextstep/src/controls.tsx) |
| Window chrome, folding and visibility | [`packages/nextstep/src/desktop/WindowFrame.tsx`](../packages/nextstep/src/desktop/WindowFrame.tsx) |
| Dock, workspace bar and menus | [`packages/nextstep/src/desktop`](../packages/nextstep/src/desktop) |
| Application layout, status and errors | [`apps/dashboard/src/shared/styles/apps.css`](../apps/dashboard/src/shared/styles/apps.css) |
| Interactive examples | [`apps/dashboard/src/features/gallery/Gallery.tsx`](../apps/dashboard/src/features/gallery/Gallery.tsx) |
| Brand mark | [`apps/dashboard/public/favicon.svg`](../apps/dashboard/public/favicon.svg) |

- **Surfaces & contrast:** Neutral gray surfaces, thin outlines and hard bevels (raised actionable controls, recessed editable areas). Active title bars are dark with white text. Keep selection and keyboard focus visually distinct, and pair meaningful color with text or a mark.
- **Typography & layout:** Ordinary weight for labels, short stronger headings, monospace for paths/code/aligned data; allow wrapping at narrow widths. Use definition lists for one object's fields and tables for comparable records. Give icon-only controls accessible names, tooltips and touch targets; verify crisp edges at integer and fractional DPR.
- **Window chrome:** Minimize on left, close on right, folding separate from maximizing, resize controls available on resizable desktop windows. Use shared `Tabs` for switching sections and `ButtonGroup` for related modes/filters while preserving work and focus.

### Settings and disclosures

- Group related settings with a compact heading and thin divider; stack fields based on window width and keep commit actions reachable while settings scroll.
- Use native `<details>` with shared `ns-disclosure` styling (full-row toggle, visible focus, trailing chevron). Put core workflow choices and consequences in the main form; place optional tuning and raw technical IDs/checksums in disclosures.

## Interaction conventions

- **Visibility & input:** Folded, minimized and off-workspace windows retain mounted state (drafts, selection, scroll) while remaining inert to input and focus. Give drag, double-click and context-menu actions visible alternatives.
- **Menus & dialogs:** Commands bind to the window and resource active when opened. Modal dialogs trap focus and restore it on dismissal; Inspectors are non-modal. Menus support arrow keys, `Enter`, `Escape` and visible Back navigation on narrow screens.
- **Async resilience:** Refreshing/polling preserves drafts, selection and reading position; failed observations mark retained data stale rather than clearing it. Startup and lazy views load in place with local **Retry** on failure and never auto-reload over dirty drafts.
- **Target clarity:** Always identify the execution target (host vs VM, exact VM/project/run) near destructive or execution actions, and confirm destructive operations (VM delete, volume delete, End Session, discard draft) explicitly.

### Interface copy

Let layout explain routine structure; avoid redundant helper prose.

- Use short, specific action labels (reserve ellipses for progress/truncation; use **More** for secondary item actions).
- Explain restrictions, stale observations, recovery steps and effects on running work or storage at the decision point.
- Keep routine success quiet or brief; keep errors and uncertain outcomes visible beside their affected draft/action until addressed.
- Preserve accessible labels, status announcements and clear names for icon-only actions.

## Application design boundaries

Start from an existing AXV feature frame and keep common patterns consistent:

- **Primary action first:** Put the main action and current target at the top of lists/forms; keep secondary diagnostics, IDs and raw metadata in **Details** disclosures.
- **Editors & terminals:** Start VM files read-only with explicit **Edit** and reviewed **Save**; keep terminals in dark monospace inside shared gray chrome with clear detach/reconnect/end/continue distinctions.
- **Operational panels (VMs, Codex, Tasks, Remote, Notifications, GitHub Review):** Distinguish loading, offline/stale, empty and error states; keep recovery actions bound to original request/target identities; never treat local listening or provider acceptance as public reachability or device delivery.

## Narrow screens and accessibility

- Fit windows to the usable visual viewport (including when software keyboards shrink height) and restore saved desktop geometry at wider widths.
- Keep active workspace controls, Window Overview, Dock add controls and primary view actions reachable on narrow/touch screens; separate touch scrolling from item activation.
- Use semantic labels, visible keyboard focus, non-color state indicators and `prefers-reduced-motion` support.

## Acceptance checks

- Verify collapsed/expanded disclosures, narrow widths, fractional DPR and touch across the [Playwright browser projects](../playwright.config.ts).
- Exercise loading, empty, error, disabled and dirty/recovery states, confirming draft, selection and scroll retention across tab/view switches, folding and minimizing.
- Check keyboard and focus return on menu/dialog dismissal, cancel and retry.
- For VM UI flow/copy changes, run `make test-vm-ui` ([focused VM UI review](development.md#focused-vm-ui-review)). Record viewport, scale, browser and interaction state in PR evidence; keep screenshots outside commits.
