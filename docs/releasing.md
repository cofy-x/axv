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

[THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) owns distribution boundaries and upstream sources. The [collector](../scripts/release-notices.mjs) reads linked Go modules from the executable, requires their versions to match the current server module graph, verifies source checksums and rehashes cached sources with `go mod verify` before retaining notices. It includes Go's license and bundled-library notices, the installed JavaScript production graph from `pnpm licenses list --prod --json --recursive`, and Vite/Rolldown notices for generated runtime helpers. [pnpm documents the inventory command](https://pnpm.io/cli/licenses); [Go documents cache verification](https://go.dev/ref/mod#go-mod-verify).

Review the generated `licenses/manifest.json`, the original notices and relevant source-file terms before publication. Pay attention to inherited third-party notices, new or unknown licenses, and changes to compiled versus external dependencies. Missing license text or module-checksum mismatches stop packaging. Do not label the whole archive, upstream tools or guest OS as MIT.

## Review and publish

Publication is an explicit maintainer action after candidate review. The first publication should be a GitHub prerelease while experimental capabilities remain under evaluation.

1. Confirm the archive's `axv version`, `build-info.json` revision and clean-source setting match the reviewed commit, workflow run and passing CI checks. Inspect `SHA256SUMS` and dependency notices. Retain native acceptance evidence for the actual claims and platform.
2. Write release notes with the version, source commit, changes, platform/architecture, installation and checksum instructions, runtime prerequisites, experimental or unavailable capabilities, and any incompatible store baseline changes. Link the owning startup and security references. Report which native daemon, VM and real-model checks were performed; do not infer them from fixture tests.
3. Create an annotated or signed `v<version>` tag at that exact commit after review. Push the tag and create a **draft** GitHub Release for it. Upload the reviewed archive and `SHA256SUMS`; preserve its original filenames and license files. Mark prereleases appropriately. Review the draft, then publish deliberately. The candidate workflow does not perform these actions.
4. Do not replace a published tag or archive with different bytes. Publish a new version for corrections. Keep release notes and published availability links accurate without inventing downloads for platforms that have no reviewed artifact.

For a draft through GitHub's web interface, follow [managing releases](https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository). [Workflow artifacts](https://docs.github.com/en/actions/concepts/workflows-and-actions/workflow-artifacts) have finite retention; promote approved files to the draft rather than treating the Actions download URL as a permanent distribution endpoint.

## VM image publication

Application packaging does not publish VM images or promote upstream bases to AXV releases. The [image recipes](../images/guest/README.md#per-machine-initialization-and-release-work) own generated guest notices, OS package inventories, source/provenance review, identity cleanup and native acceptance. Keep their exported `distribution/` materials with each exact provider/architecture bundle; guest and OS components retain their own terms. A completed builder run does not approve an image for publication.

The manual [Guest image candidate workflow](../.github/workflows/guest-images.yml) builds a native Firecracker amd64 bundle from an explicitly supplied, reviewed Ubuntu 24.04 OCI digest. It uses the pinned Go and Node toolchains, then verifies and packages the disk, manifest, base identity and exported distribution materials. Its repository permission is read-only; the workflow neither starts a VM nor publishes a release. Run the separate native acceptance against the exact downloaded bundle before promoting it.

To package an existing image-builder output locally:

```sh
VERSION=0.1.0-rc.1 node scripts/package-guest-release.mjs --bundle /absolute/new-firecracker-bundle
```

This requires a clean checkout and matching clean guest-binary VCS metadata. The output in `dist/guest-release/<version>/<provider>_<arch>/` contains the complete compressed bundle, a separately named raw disk for catalog downloads, and `candidate.json` with their source/build identities; `SHA256SUMS` covers all three. The packager validates the current guest contract through the same Go owner as image building and importing, then assembles the archive and disk from a private snapshot whose files match the verified input checksums. It rejects unlisted files, symlinks, changed bytes, missing license/package inventories and mismatched guest capabilities or build metadata. It refuses to overwrite an existing candidate.

For implementation testing with uncommitted changes, add `--local`. This writes an explicitly `localOnly` candidate beneath this checkout's `.data/test/guest-candidate-*`; it is retained for inspection and cannot establish release readiness or an official catalog entry. Review and remove only that generated directory when finished. Dirty source metadata remains visible.

Upload the reviewed disk and complete bundle alongside their checksum manifest to the same immutable prerelease. Preserve provider/architecture filenames, exported notices and the exact bundle used for native acceptance. Only after those downloads exist should a reviewed `artifacts.Official` entry bind the actual disk URL, digest and guest contract. Selecting a new default enhanced image remains an explicit product decision.

Publish only those provider/architecture artifacts that have passed their own checks. A reviewed versioned catalog entry must reference the actual immutable artifacts and digests. Update availability in the README and affected references only when the artifacts exist.
