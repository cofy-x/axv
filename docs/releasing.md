# Releases and distribution

This page owns release preparation, version selection, candidate artifacts and publication checks. [Development](development.md#verification) owns the required checks; [project status](../README.md#project-status) owns current availability and native validation. An Actions artifact is a candidate for review, not a published release.

## Version and source

Use `major.minor.patch`, with an optional prerelease suffix such as `0.1.0-rc.1`. The three private JavaScript manifests share the base version; update them together when selecting a new release series. Their `private` fields stay enabled: these packages compose the application and are not published to npm. `VERSION` supplies the full version to the production build and must be repeated for smoke, browser tests and packaging. An ordinary source build still reports `dev`.

Prepare releases from a reviewed commit on `main`, with passing required CI checks and no uncommitted changes. Confirm the current README's availability claims and document any user-visible changes, known limitations and incompatible AXV-store baseline changes in the release notes. Never describe a hosted desktop smoke as native VM or real-model acceptance.

## Build a candidate

The manual [Release candidate workflow](../.github/workflows/release.yml) builds and smoke-tests a native **Linux amd64** executable on Ubuntu, then uploads a candidate archive and its checksum manifest. It has read-only repository permissions and does not create tags, GitHub Releases or VM images. Select **Actions → Release candidate → Run workflow**, choose `main` and supply the intended version, for example `0.1.0-rc.1`. The version input is passed through the environment rather than interpolated into shell code.

For local preparation on native Linux amd64 with the [pinned toolchain](../README.md#install-and-use):

```sh
make deps
VERSION=0.1.0-rc.1 node scripts/package-release.mjs --check-version
VERSION=0.1.0-rc.1 make build
VERSION=0.1.0-rc.1 make smoke-production
VERSION=0.1.0-rc.1 node scripts/package-release.mjs
```

Run all required [verification](development.md#verification) for the selected commit before approving a release. The candidate workflow covers build and smoke, while normal CI covers unit, static and browser checks. Explicit native daemon, VM and real-Codex checks remain separate and apply to the capabilities claimed in that release.

The packager uses [the production build](development.md#production-build), verifies the binary's version, production tag, target, CGO setting and pinned Go version, and writes a new directory at `dist/release/<version>/`. It requires a clean Git checkout, including tracked and untracked source files, and rejects missing VCS metadata, a binary from a different `HEAD`, or `vcs.modified` other than `false`. It checks the checkout again after assembly; ordinary ignored build output is allowed. The workflow also checks source cleanliness after dependency installation, so changed dependency checksums must be reviewed and committed before building a candidate. Never hide these changes or disable VCS stamping.

The packager refuses to overwrite an existing candidate directory. After reviewing or moving an older candidate, remove only that generated version directory explicitly if you need to rebuild it. Temporary assembly and helper execution use this checkout's [test isolation](development.md#test-isolation-and-diagnostics).

| Output | Contents |
| --- | --- |
| `axv_<version>_linux_amd64.tar.gz` | Executable with embedded UI, MIT license, third-party policy and original dependency license files, dependency inventory, source/build metadata and startup pointers |
| `SHA256SUMS` | SHA-256 of the archive as distributed |

The archive keeps executable permissions inside its tar container. No Node runtime, VM base, kernel, Lima/Firecracker executable, Codex installation or account credentials are included. Linux amd64 is the initial automated packaging target; add a platform only after a native build/smoke job and the relevant acceptance checks are available. Source support and existing native-validation claims remain in the README.

Download the artifact from its completed Actions run; GitHub wraps the archive and manifest in an artifact download. Extract that wrapper first, then verify the original archive before extracting or running it:

```sh
sha256sum --check SHA256SUMS
tar -xzf axv_0.1.0-rc.1_linux_amd64.tar.gz
./axv_0.1.0-rc.1_linux_amd64/axv version
```

On macOS, use `shasum -a 256 -c SHA256SUMS` to inspect the checksum; this Linux executable still needs a Linux amd64 host. Checksums detect changed bytes; obtain the manifest from the same trusted release or workflow run. The candidate workflow does not currently sign or notarize artifacts or generate cryptographic provenance attestations.

## License review

[THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) owns distribution boundaries and upstream sources. The [collector](../scripts/release-notices.mjs) takes linked Go modules from `go version -m -json`, verifies their source checksums and retains original notice files. It includes Go's license and bundled-library notices, uses `pnpm licenses list --prod --json --recursive` for the installed JavaScript production dependency graph, and preserves Vite/Rolldown notices for generated runtime helpers. [pnpm documents this inventory command](https://pnpm.io/cli/licenses); [Go documents binary module inspection](https://go.dev/ref/mod#go-version-m).

Review the generated `licenses/manifest.json`, the original notices and relevant source-file terms before publication. Pay attention to inherited third-party notices, new or unknown licenses, and changes to compiled versus external dependencies. Missing license text or module-checksum mismatches stop packaging. Do not label the whole archive, upstream tools or guest OS as MIT.

## Review and publish

Publication is an explicit maintainer action after candidate review. The first publication should be a GitHub prerelease while experimental capabilities remain under evaluation.

1. Confirm the archive's `axv version`, `build-info.json` revision and clean-source setting match the reviewed commit, workflow run and passing CI checks. Inspect `SHA256SUMS` and dependency notices. Retain native acceptance evidence for the actual claims and platform.
2. Write release notes with the version, source commit, changes, platform/architecture, installation and checksum instructions, runtime prerequisites, experimental or unavailable capabilities, and any incompatible store baseline changes. Link the owning startup and security references. Report which native daemon, VM and real-model checks were performed; do not infer them from fixture tests.
3. Create an annotated or signed `v<version>` tag at that exact commit after review. Push the tag and create a **draft** GitHub Release for it. Upload the reviewed archive and `SHA256SUMS`; preserve its original filenames and license files. Mark prereleases appropriately. Review the draft, then publish deliberately. The candidate workflow does not perform these actions.
4. Do not replace a published tag or archive with different bytes. Publish a new version for corrections. Keep release notes and published availability links accurate without inventing downloads for platforms that have no reviewed artifact.

For a draft through GitHub's web interface, follow [managing releases](https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository). [Workflow artifacts](https://docs.github.com/en/actions/concepts/workflows-and-actions/workflow-artifacts) have finite retention; promote approved files to the draft rather than treating the Actions download URL as a permanent distribution endpoint.

## VM image publication

Application packaging does not publish VM images or promote the current upstream bases to AXV releases. Follow [image recipes](../images/guest/README.md#per-machine-initialization-and-release-work) for each actual provider and architecture. Before publication, retain the base URL/digest or OCI digest, package inventory, exact guest-service source and image manifest, rootfs/kernel checksums and applicable upstream notices/corresponding source. Verify the image contains no machine identity, host keys, tokens or user data, then complete native lifecycle, enhanced-guest and SSH acceptance for that exact bundle.

Publish only those provider/architecture artifacts that have passed their own checks. A reviewed versioned catalog entry must reference the actual immutable artifacts and digests. Update availability in the README and affected references only when the artifacts exist.
