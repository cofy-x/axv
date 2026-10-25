# Releases and distribution

This page owns release preparation, version selection, candidate artifacts and publication checks. [Development](development.md#verification) owns the required checks; [project status](../README.md#project-status) owns current availability and native validation. An Actions artifact is a candidate for review, not a published release.

## Version and source

Use `major.minor.patch`, with an optional prerelease suffix such as `0.1.0-rc.1`. The three private JavaScript manifests share the base version; update them together when selecting a new release series. Their `private` fields stay enabled: these packages compose the application and are not published to npm. `VERSION` supplies the full version to the production build and must be repeated for smoke, browser tests and packaging. An ordinary source build still reports `dev`.

Prepare releases from a reviewed commit on `main`, with passing required CI checks and no uncommitted changes. Merge release-preparation changes before selecting that commit. Confirm the current README's availability claims and document any user-visible changes, known limitations and [AXV-store compatibility policy](storage.md#desktop-database-baseline) in the release notes. Never describe a hosted desktop smoke as native VM or real-model acceptance.

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

The packager uses [the production build](development.md#production-build), verifies the binary's version, complete production build tags, target, CGO setting and pinned Go version, and writes a new directory at `dist/release/<version>/`. It requires a clean Git checkout, including tracked and untracked source files, and rejects missing VCS metadata, a binary from a different `HEAD`, or `vcs.modified` other than `false`. It checks the checkout again after assembly; ordinary ignored build output is allowed. The workflow also checks source cleanliness after dependency installation, so changed dependency checksums must be reviewed and committed before building a candidate. Never hide these changes or disable VCS stamping.

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

The [production tags](development.md#production-build) exclude Tailscale's separate node-management Web UI. Packaging also rejects a binary that still links its prebuilt frontend module; the Go module's top-level license alone does not establish the terms of its embedded assets. Follow the [distribution boundary](../THIRD_PARTY_NOTICES.md#application-distributions) before including any such bundle.

## Review and publish

Publication is an explicit maintainer action after candidate review. Use the existing manual process for the first Linux amd64 prerelease; no additional release service is required. Guest images, build provenance attestations, CodeQL and Socket are separate work, not prerequisites for publishing this application archive. Required CI and the acceptance for capabilities claimed in the release still apply.

### Preserve and approve the candidate

1. Check GitHub tags, releases (including drafts visible to a maintainer) and candidate runs before selecting an unused version. Check that the local remote addresses the intended repository. Record the candidate run's actual `head_sha`; a later `main` HEAD is not a substitute. Confirm that SHA is reviewed and has passing required CI checks.
2. Download the successful candidate before its artifact expires. Preserve the original archive and `SHA256SUMS` together, outside directories that later build or cleanup commands overwrite. Inspect and extract copies. Do not rebuild, recompress, regenerate the manifest or edit the approved files during promotion. The Actions wrapper ZIP has its own size and digest; neither describes the inner archive.
3. Record the following in the release PR or release notes, keeping validation history out of this reference:

   | Evidence | Required record |
   | --- | --- |
   | Source and build | Version, full source SHA, candidate run URL and attempt, artifact ID, and required CI run/check links for that SHA |
   | Files | Exact filename, byte count and SHA-256 for **each** original file, including `SHA256SUMS` itself |
   | Review | Matching `axv version`, `build-info.json` revision and `vcs.modified=false`; dependency inventory and original-license review |
   | Acceptance | Commands, environment, result and file digest for the downloaded Linux amd64 archive; separately identified native daemon, real-agent and VM evidence used in the notes |

4. Verify `SHA256SUMS`, then follow [release archive installation](startup.md#install-a-release-archive) with a new version directory and isolated `AXV_HOME`. Test the installed original's version and foreground startup. Test the original in a native systemd user session before claiming background-startup acceptance for this release. Fixture smoke, native platform checks and real Agent/VM acceptance establish different evidence. Label historical Agent/VM evidence with its source commit and environment, including any locally built image; do not present it as acceptance of this downloaded archive.
5. Write notes covering the fixed source link, changes, Linux amd64 target, [installation and runtime prerequisites](startup.md#install-a-release-archive), checksum instructions, experimental or unavailable capabilities, data compatibility and [security policy](../SECURITY.md). State which checks used this archive and which remain pending. Do not claim an official Guest image or live provider deployment without its own evidence.

[Workflow artifacts](https://docs.github.com/en/actions/concepts/workflows-and-actions/workflow-artifacts) expire. Retain the approved originals through publication and verification; their Actions URLs are not permanent download endpoints. The recorded association establishes review traceability, not cryptographic proof that a workflow built those bytes.

### Prepare the tag and draft

Enable repository [release immutability](https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/establish-provenance-and-integrity/prevent-release-changes) **before creating the new draft** and confirm the effective setting. It applies to future releases. Do not assume an older release acquired protection when the setting changed.

Create an annotated `v<version>` tag at the exact approved SHA; use a signed tag when maintainer signing is configured. Push only that tag. For example, with `TAG` and `SOURCE_SHA` set to the selected tag and full commit SHA:

```sh
git tag -a "$TAG" "$SOURCE_SHA" -m "AXV $TAG"
git push origin "refs/tags/$TAG"
git ls-remote origin "refs/tags/$TAG" "refs/tags/$TAG^{}"
```

The remote annotated tag must peel (`^{}`) to `SOURCE_SHA`; stop on a missing or different result. Repeat this remote check immediately before publication. [`gh release create --verify-tag`](https://cli.github.com/manual/gh_release_create) checks that the remote tag exists, not that it points to the reviewed commit. Without that flag, the CLI can create a missing tag from the default branch.

Set `ARCHIVE` and `SUMS` to the preserved original files and `NOTES` to the reviewed notes file. Explicitly retain the draft and prerelease flags:

```sh
gh release create "$TAG" "$ARCHIVE" "$SUMS" \
  --repo cofy-x/axv --verify-tag --draft --prerelease --latest=false \
  --notes-file "$NOTES"
```

The [web interface](https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository) is also suitable if the same checks are completed. Preserve filenames and all bundled license materials. An application-only draft needs exactly the archive and `SHA256SUMS`. GitHub's automatically generated source downloads are separate from these uploaded assets. Any additional assets must be explicitly reviewed before publication. [Each asset must be smaller than 2 GiB](https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases#storage-and-bandwidth-quotas), meaning fewer than `2147483648` bytes; the limit is per file, not the combined release size.

Inspect the draft through the authenticated Releases API and record its numeric release ID. Require the intended tag, `draft=true`, `prerelease=true` and an exact asset inventory. For each asset, record its numeric ID, filename, `state=uploaded`, byte count and `digest`; compare sizes and any supplied SHA-256 digest with the preserved originals. Use a maintainer identity that can access drafts; anonymous public download URLs do not establish draft readiness.

Download each asset by its ID into a fresh inspection directory using the [asset API](https://docs.github.com/en/rest/releases/assets#get-a-release-asset), for example:

```sh
gh api -H 'Accept: application/octet-stream' \
  "repos/cofy-x/axv/releases/assets/$ASSET_ID" > "$DOWNLOADED_FILE"
```

Require a successful download and compare **both** files with the originals using `cmp`; independently check their byte counts and SHA-256 values. Then run `sha256sum --check SHA256SUMS` in the download directory and repeat the installation/version smoke from those files. A matching archive against a modified manifest is insufficient. Re-read the draft inventory after inspection and stop if another writer changed it.

### Publish and verify

After the final asset, notes and remote-tag checks pass, [publish the draft](https://cli.github.com/manual/gh_release_edit) as a separate deliberate maintainer action:

```sh
gh release edit "$TAG" --repo cofy-x/axv --verify-tag \
  --draft=false --prerelease --latest=false
```

Confirm `draft=false` and `immutable=true` through the release API. GitHub [locks the tag and assets on publication](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases); a draft remains editable until then. Verify the published release and each original file with [`gh release verify`](https://cli.github.com/manual/gh_release_verify) and [`gh release verify-asset`](https://cli.github.com/manual/gh_release_verify-asset):

```sh
gh release verify "$TAG" --repo cofy-x/axv
gh release verify-asset "$TAG" "$ARCHIVE" --repo cofy-x/axv
gh release verify-asset "$TAG" "$SUMS" --repo cofy-x/axv
```

Download both public assets into a new directory, compare them with the preserved originals, verify the checksum, and repeat installation/version smoke. Update README availability and versioned download links only after these checks pass. GitHub's release attestation binds the published tag, commit and assets; it does not attest which build workflow produced the candidate.

If creation, upload or publication times out or fails, query the remote tag, release and asset state before retrying. Keep a failed review as a draft; repair partial or incorrect uploads there and repeat the complete asset checks. If a command actually published the release, use the published-release rules. Never move a published tag or replace its files: issue a new version for corrections, and describe the problem in the notes. Deleting an immutable release does not permit reuse of its tag name. Report failed post-publication verification and keep availability links pending; do not silently treat publication as success.

## VM image publication

Application packaging does not publish VM images or promote upstream bases to AXV releases. The [image recipes](../images/guest/README.md#per-machine-initialization-and-release-work) own generated guest notices, OS package inventories, source/provenance review, identity cleanup and native acceptance. Keep their exported `distribution/` materials with each exact provider/architecture bundle; guest and OS components retain their own terms. A completed builder run does not approve an image for publication.

The manual [Guest image candidate workflow](../.github/workflows/guest-images.yml) builds a native Firecracker amd64 bundle from an explicitly supplied, reviewed Ubuntu 24.04 OCI digest. It uses the pinned Go and Node toolchains, then verifies and packages the disk, manifest, base identity and exported distribution materials. Its repository permission is read-only; the workflow neither starts a VM nor publishes a release. Run the separate native acceptance against the exact downloaded bundle before promoting it.

To package an existing image-builder output locally:

```sh
VERSION=0.1.0-rc.1 node scripts/package-guest-release.mjs --bundle /absolute/new-firecracker-bundle
```

This requires a clean checkout and matching clean guest-binary VCS metadata. The output in `dist/guest-release/<version>/<provider>_<arch>/` contains the complete compressed bundle, a separately named raw disk for catalog downloads, and `candidate.json` with their source/build identities; `SHA256SUMS` covers all three. The packager validates the current guest contract through the same Go owner as image building and importing, then assembles the archive and disk from a private snapshot whose files match the verified input checksums. It rejects unlisted files, symlinks, changed bytes, missing license/package inventories and mismatched guest capabilities or build metadata. It refuses to overwrite an existing candidate.

For implementation testing with uncommitted changes, add `--local`. This writes an explicitly `localOnly` candidate beneath this checkout's `.data/test/guest-candidate-*`; it is retained for inspection and cannot establish release readiness or an official catalog entry. Review and remove only that generated directory when finished. Dirty source metadata remains visible.

Publish Guest artifacts through a separately reviewed draft prerelease with its own unused version and tag. Upload the reviewed disk, complete bundle, `candidate.json` and original `SHA256SUMS` before final review. Application and Guest candidates each use that checksum filename; keep their approved files in their respective releases. Do not append Guest assets to a published immutable application release. Preserve provider/architecture filenames, exported notices and the exact bundle used for native acceptance, and apply the same asset-size and download checks before publication. Only after those downloads exist should a reviewed `artifacts.Official` entry bind the actual disk URL, digest and guest contract. Selecting a new default enhanced image remains an explicit product decision.

Publish only those provider/architecture artifacts that have passed their own checks. A reviewed versioned catalog entry must reference the actual immutable artifacts and digests. Update availability in the README and affected references only when the artifacts exist.
