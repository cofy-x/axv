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
- the installed production JavaScript dependency graph reported by pnpm, including transitive dependencies and conservatively retained type packages.
- Vite and Rolldown, whose generated preload and interoperability helpers become part of the embedded UI.

The collector preserves upstream text and refuses a dependency without a license file. The JavaScript list describes the production dependency graph, rather than claiming every package survives frontend tree shaking. Review both the inventory and original texts when changing dependencies or preparing a distribution. Dependencies can carry additional terms inside source files; the collector supports that review and does not replace it.

The [release procedure](https://github.com/cofy-x/axv/blob/main/docs/releasing.md) owns generation and review. Keep this file and `licenses/` with a redistributed application archive. A source checkout does not include downloaded dependency trees; obtain their original terms from the exact locked packages and module sources.

Project Files uses [CodeMirror 6](https://codemirror.net/) and its CodeMirror/Lezer packages for syntax-aware source views and mapping reading positions after external changes. These packages retain their upstream MIT licenses. Their exact versions are locked in the JavaScript dependency graph above; the release collector includes their original license files with the application distribution.

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
