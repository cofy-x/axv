# Third-party software

AXV's own source is licensed under the [MIT License](LICENSE). Dependencies, downloaded tools and VM contents retain their own copyrights and licenses. AXV's MIT license does not relicense them.

## Visual references

The `mesh-network` icon is original AXV artwork under the MIT License. Its node-matrix concept was informed by the [official Tailscale icon](https://github.com/tailscale/tailscale/blob/main/client/web/src/assets/icons/tailscale-logo.svg); AXV includes no upstream SVG paths or artwork files. The referenced source repository declares [BSD-3-Clause](https://github.com/tailscale/tailscale/blob/main/LICENSE); Tailscale's name and logo remain its own marks.

The `cloud-tunnel` icon used by the Cloudflare utility is original AXV artwork under the MIT License. Its generic cloud and tunnel drawing includes no Cloudflare logo or third-party artwork. Cloudflare's name and marks remain its own.

## Application distributions

The production executable embeds a JavaScript UI and links Go dependencies. Their exact versions belong to [pnpm-lock.yaml](https://github.com/cofy-x/axv/blob/main/pnpm-lock.yaml) and [go.mod](https://github.com/cofy-x/axv/blob/main/apps/server/go.mod), with the Go binary's build information recording the modules actually linked for that platform.

Release candidate archives include `licenses/manifest.json` and the original license, copyright, patent and notice files collected from:

- Go's standard library and bundled libraries;
- the exact Go module versions recorded in the executable, including inherited notices such as modernc's SQLite and libc notices;
- separately licensed data and diagnostic assets embedded by those Go modules, with their pinned source locations;
- the installed production JavaScript dependency graph reported by pnpm, including transitive dependencies and conservatively retained type packages.
- Vite and Rolldown, whose generated preload and interoperability helpers become part of the embedded UI.

The collector preserves upstream text and refuses a dependency without a license file. The JavaScript list describes the production dependency graph, rather than claiming every package survives frontend tree shaking. Review both the inventory and original texts when changing dependencies or preparing a distribution. Dependencies can carry additional terms inside source files; the collector supports that review and does not replace it.

Production builds use Tailscale's upstream [`ts_omit_webclient` option](https://github.com/tailscale/tailscale/blob/v1.104.1/ipn/ipnlocal/web_client_stub.go) to exclude its separate node-management Web UI and bundled frontend assets. AXV supplies its own desktop and Tailscale controls over the embedded node. The collector rejects a binary that still links `github.com/tailscale/web-client-prebuilt`; distributing that separate frontend would require review and original notices for its JavaScript, fonts and other assets in addition to the Go module's license.

The [supplemental notice inventory](third_party/notices/manifest.json) records original licenses and source locations for HTMX diagnostic assets bundled by Tailscale and the Public Suffix List data used by `golang.org/x/net/publicsuffix`. HTMX's original 0BSD terms are retained. The Public Suffix List source is available under MPL-2.0 at the pinned source URL in the inventory, alongside links to the upstream generated tables. The collector requires the reviewed Go module versions and unchanged license texts before retaining these materials in `licenses/manifest.json`; review the inventory and source availability when updating either module.

The [release procedure](https://github.com/cofy-x/axv/blob/main/docs/releasing.md) owns generation and review. Keep this file and `licenses/` with a redistributed application archive. A source checkout does not include downloaded dependency trees; obtain their original terms from the exact locked packages and module sources.

Project Files uses [CodeMirror 6](https://codemirror.net/) and its CodeMirror/Lezer packages for syntax-aware source views and mapping reading positions after external changes. These packages retain their upstream MIT licenses. Their exact versions are locked in the JavaScript dependency graph above; the release collector includes their original license files with the application distribution.

Terminal views use [xterm.js 6.0.0](https://github.com/xtermjs/xterm.js/tree/6.0.0), with its original [MIT license](https://github.com/xtermjs/xterm.js/blob/6.0.0/LICENSE) retained by the release collector. AXV applies a [pinned dependency patch](patches/@xterm__xterm@6.0.0.patch) for empty alternate-screen buffers losing their final row after resizing ([upstream issue #6163](https://github.com/xtermjs/xterm.js/issues/6163)). The one-condition fix follows [upstream PR #6164 at revision `86d30d7`](https://github.com/xtermjs/xterm.js/pull/6164/commits/86d30d7e88968d27b898838cf1b1885cdad109ea) and applies to the package source and both distributed JavaScript entry points; upstream license and copyright text remain unchanged. [Development](docs/development.md#dependency-patches) owns patch maintenance and removal.

Cloudflare Access verification uses [go-oidc](https://github.com/coreos/go-oidc) and [go-jose](https://github.com/go-jose/go-jose), both under Apache-2.0. Their exact versions and original notices follow the Go module and release-inventory rules above.

## Separately installed software and VM images

The application archive does not bundle the following tools, base images or kernels. Setup may download selected tools or images separately, and guest-image recipes combine upstream packages with AXV's guest service.

| Component | Original license information | Distribution boundary |
| --- | --- | --- |
| Lima | [Lima LICENSE](https://github.com/lima-vm/lima/blob/master/LICENSE), Apache-2.0, plus notices accompanying each release | Preserve the downloaded release's own notices when redistributing it. |
| Firecracker and jailer | [Firecracker LICENSE](https://github.com/firecracker-microvm/firecracker/blob/main/LICENSE), Apache-2.0, with additional upstream notices | Review the exact administrator-provisioned release before redistribution. |
| Ubuntu base images and packages | [Canonical's intellectual property policy](https://canonical.com/legal/intellectual-property-policy) and package-specific copyright files | Ubuntu is an aggregate of separately licensed components. Preserve `/usr/share/doc/*/copyright`, package versions, base identity and applicable source-code obligations. |
| Linux kernel | [Linux kernel licensing rules](https://www.kernel.org/doc/html/latest/process/license-rules.html), GPL-2.0-only with documented exceptions | A separately supplied kernel needs its own provenance, configuration, notices and corresponding-source compliance. |
| tmux | [tmux COPYING](https://github.com/tmux/tmux/blob/master/COPYING) and individual source-file notices | Installed by the operator for Host Codex; not included in AXV's archive. |
| Codex CLI | [Codex LICENSE](https://github.com/openai/codex/blob/main/LICENSE) and the selected release's notices | Installed and authenticated by the operator; not included in AXV's archive. |
| cloudflared | [cloudflared LICENSE](https://github.com/cloudflare/cloudflared/blob/master/LICENSE), Apache-2.0, plus the selected release's notices | Installed and supervised by the operator for the optional Cloudflare entrypoint; not included in AXV's archive. |

VM builders include a guest-specific MIT/Go notice inventory and OS package records in the image and its exported `distribution/` directory. The [guest-image reference](https://github.com/cofy-x/axv/blob/main/images/guest/README.md#per-machine-initialization-and-release-work) owns these materials and the separate publication gate. An image's license review covers its entire contents, including base-system packages, separately from AXV's MIT-licensed additions.
