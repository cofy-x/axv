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

The [production tags](development.md#production-build) exclude Tailscale's separate node-management Web UI. Packaging also rejects a binary that still links its prebuilt frontend module; the Go module's top-level license alone does not establish the terms of its embedded assets. Follow the [distribution boundary](../THIRD_PARTY_NOTICES.md#application-distributions) before including any such bundle. Review the supplemental embedded-asset notices and confirm that their recorded source URLs still provide the reviewed revisions before publication.

## Review and publish

Publication is an explicit maintainer action after candidate review. Use this manual process for Linux amd64 prereleases; no additional release service is required. Guest images, build provenance attestations, CodeQL and Socket are separate work, not prerequisites for publishing this application archive. Required CI and the acceptance for capabilities claimed in the release still apply.

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

Use an authenticated maintainer identity with push access to [list releases, including drafts](https://docs.github.com/en/rest/releases/releases#list-releases). Select the exact intended tag, record its numeric release ID, then inspect that ID:

```sh
gh api --paginate repos/cofy-x/axv/releases \
  --jq '.[] | {id, tag_name, draft, prerelease}'
RELEASE_ID='<numeric-id-for-the-exact-tag>'
gh api "repos/cofy-x/axv/releases/$RELEASE_ID"
```

The [by-tag endpoint](https://docs.github.com/en/rest/releases/releases#get-a-release-by-tag-name) returns published releases; its `404` does not establish that no draft exists. Require the intended tag, `draft=true`, `prerelease=true` and an exact asset inventory. For each asset, record its numeric ID, filename, `state=uploaded`, byte count and `digest`; compare sizes and any supplied SHA-256 digest with the preserved originals. Anonymous public download URLs do not establish draft readiness.

Prefer `gh release edit "$TAG" --repo cofy-x/axv --notes-file "$NOTES"` for draft-note updates. The CLI preserves the current tag association when sending its update. If using a REST `PATCH` to the numeric release ID, explicitly include `tag_name` with the intended tag: omitting it can remove the release's current tag association. Re-read the tag and draft state after any update. The [CLI implementation](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/release/edit/edit.go) documents this API behavior.

Download each asset by its ID into a fresh inspection directory using the [asset API](https://docs.github.com/en/rest/releases/assets#get-a-release-asset), for example:

```sh
gh api --allow-escape-sequences -H 'Accept: application/octet-stream' \
  "repos/cofy-x/axv/releases/assets/$ASSET_ID" > "$DOWNLOADED_FILE"
```

Use the binary-output flag only with a file redirect or a pipe that saves the bytes, never an interactive terminal. The [gh 2.102.0 output guard](https://github.com/cli/cli/blob/v2.102.0/pkg/cmd/api/api.go#L491-L503) can reject valid binary data containing escape sequences even when stdout is redirected or piped.

Require a `gh` exit status of `0` and compare **both** files with the originals using `cmp`; independently check their byte counts and SHA-256 values. Then run `sha256sum --check SHA256SUMS` in the download directory and repeat the installation/version smoke from those files. A matching archive against a modified manifest is insufficient. Re-read the draft inventory after inspection and stop if another writer changed it.

### Publish and verify

After the final asset, notes and remote-tag checks pass, [publish the draft](https://cli.github.com/manual/gh_release_edit) as a separate deliberate maintainer action:

```sh
gh release edit "$TAG" --repo cofy-x/axv --tag "$TAG" --verify-tag \
  --draft=false --prerelease --latest=false
```

Keep the explicit `--tag "$TAG"`: in gh 2.102.0, `--verify-tag` only checks the remote tag when `--tag` is supplied. The positional tag selects the release to edit; it does not supply that option. This existence check still requires the separate exact-commit comparison above.

Confirm `draft=false` and `immutable=true` through the release API. GitHub [locks the tag and assets on publication](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases); a draft remains editable until then. Verify the published release and each original file with [`gh release verify`](https://cli.github.com/manual/gh_release_verify) and [`gh release verify-asset`](https://cli.github.com/manual/gh_release_verify-asset):

```sh
gh release verify "$TAG" --repo cofy-x/axv
gh release verify-asset "$TAG" "$ARCHIVE" --repo cofy-x/axv
gh release verify-asset "$TAG" "$SUMS" --repo cofy-x/axv
```

Download both public assets into a new directory, compare them with the preserved originals, verify the checksum, and repeat installation/version smoke. Update README availability and versioned download links only after these checks pass. GitHub's release attestation binds the published tag, commit and assets; it does not attest which build workflow produced the candidate.

If creation, upload or publication times out or fails, query the remote tag, release and asset state before retrying. Keep a failed review as a draft; repair partial or incorrect uploads there and repeat the complete asset checks. If a command actually published the release, use the published-release rules. Never move a published tag or replace its files: issue a new version for corrections, and describe the problem in the notes. Deleting an immutable release does not permit reuse of its tag name. Report failed post-publication verification and keep availability links pending; do not silently treat publication as success.

## VM image publication

Application packaging does not publish VM images or promote upstream bases to AXV releases. The [image recipes](../images/guest/README.md#per-machine-initialization-and-release-work) own generated guest notices, OS package inventories, source review and identity cleanup. Keep their exported `distribution/` materials with each exact provider/architecture bundle; guest and OS components retain their own terms. A completed builder run does not approve an image for publication.

The first publication path is a complete **Linux amd64 / Firecracker** enhanced Guest bundle consumed through [administrator configuration](startup.md#linux-guest-bundle-installation). Its kernel and Firecracker/jailer runtime remain separately supplied, with a fixed reviewed [acceptance input set](development.md#linux-firecracker). The candidate does not include an application executable, kernel or runtime. Automatic Linux deployment, additional platforms and a new default image policy are outside this path. The current automatic catalog consumer serves Lima; adding a Firecracker entry does not install an image on Linux, and such an entry is not a first-publication prerequisite.

### Build and retain the Guest candidate

The manual [Guest image candidate workflow](../.github/workflows/guest-images.yml) builds a native Firecracker amd64 bundle from an explicitly supplied, reviewed Ubuntu 24.04 OCI digest. It uses the pinned Go and Node toolchains, then verifies and packages the disk, manifest, base identity and exported distribution materials. Its repository permission is read-only; it neither starts a VM nor publishes a release. A workflow definition is not a completed candidate: record an actual successful run, attempt, source SHA and artifact ID before review. The uploaded candidate has **14-day retention**; download and preserve its originals before expiry.

Choose an independent Guest version and an unused `v<version>` tag after checking remote tags, releases including drafts, and candidate runs. For example, `0.1.0-guest.1` and `v0.1.0-guest.1` are proposed values, not reserved or published names; recheck availability before use. The candidate version must satisfy the package base-version check. Do not reuse the frozen application `v0.1.0-rc.5` tag or append assets to that release.

To package an existing image-builder output locally:

```sh
VERSION=0.1.0-guest.1 node scripts/package-guest-release.mjs --bundle /absolute/new-firecracker-bundle
```

This requires a clean checkout and matching clean guest-binary VCS metadata. The output in `dist/guest-release/<version>/<provider>_<arch>/` has four core release assets:

| Asset | Contents |
| --- | --- |
| `axv_guest_<version>_firecracker_amd64.tar.gz` | Complete disk, `manifest.json`, base identity, bundle checksums and exported distribution materials |
| `axv_guest_<version>_firecracker_amd64.ext4` | Byte-identical raw rootfs, independently usable only with its image contract and the separate kernel/runtime prerequisites |
| `candidate.json` | Exact image contract, base identity, archive/disk names and source/build metadata |
| `SHA256SUMS` | SHA-256 of the other three assets |

The packager checks every release asset is a regular file **smaller than 2 GiB** (`2147483648` bytes), including the raw disk before assembly. The Firecracker recipe creates a fresh 2047 MiB disk to satisfy that limit; [image capacity](../images/guest/README.md#firecracker-root-filesystem) explains the rebuild and acceptance obligations. Sparse allocation does not reduce a raw asset's logical upload size. Compression and splitting do not make a raw-disk consumer understand another format. The archive supports the explicit download/extract operator path; the host still consumes its extracted raw disk.

The packager validates the current guest contract through the same Go owner as image building and importing, then assembles the archive and disk from a private snapshot whose files match the verified input checksums. It rejects unlisted files, symlinks, changed bytes, missing license/OS materials and mismatched guest capabilities or build metadata. It refuses to overwrite an existing candidate. For implementation testing with uncommitted changes, add `--local`; this retains an explicitly `localOnly` candidate beneath this checkout's `.data/test/guest-candidate-*`. Dirty source metadata remains visible, and this result cannot establish official release readiness.

### Review the exact Guest files

Apply the [candidate preservation and approval](#preserve-and-approve-the-candidate) process to all four Guest assets. Preserve the original archive, raw disk, `candidate.json` and `SHA256SUMS` together; record each original's filename, byte count and SHA-256, including the checksum manifest itself. The Actions wrapper ZIP is separate. Extract inspection copies, verify both checksum manifests and compare the extracted disk with the standalone raw asset. Do not rebuild, recompress, edit materials or regenerate checksums during promotion.

Before approving a draft, complete all of these gates against that exact candidate:

1. Confirm clean source/build metadata, base OCI digest, image contract 1, guest protocol 3, full capability set and the guest executable's own digest. Keep the digest-derived image ID. Application and guest executable digests are independent.
2. Review the Guest executable's original notices and the separate OS materials under `distribution/axv-guest/os-distribution/`. Resolve every source-package/version entry's license and source retrieval obligations, including base-image terms. APT index identities and a `reviewRequired` inventory are inputs to that review, not corresponding source or approval. Retain completed review and source-access evidence in the release PR/notes; provide any legally required source materials before approving publication. The application archive's review does not cover this disk.
3. Inspect the raw filesystem itself for generic identity: cleared machine IDs and global SSH host keys; no AXV machine configuration/token, dedicated SSH identity/client authorizations, initialized task owner or used task journal. Compare on-disk guest executable/manifest and retained notices with the declared distribution materials. Sidecar checksums alone do not prove the disk was sanitized.
4. Verify the independent kernel/runtime prerequisites, including exact source/configuration retrieval and notices. Host device/configuration inspection does not establish delegated cgroups or guest task enforcement.
5. Run [Linux native acceptance](development.md#linux-firecracker) using this downloaded bundle and an explicitly selected, verified published application executable. For the first Guest release, use **`--executable /absolute/axv_0.1.0-rc.5_linux_amd64/axv`**; later releases must record their own reviewed application/Guest combination. Cover lifecycle, enhanced Guest/SSH, project files, persistent tasks and controller/guest/VM recovery, plus initialization, preview and backup/restore for the declared capabilities. Include real in-guest repository dependency installation/build/tests and resource/space failure checks. Record all input digests, command, environment, results and cleanup. Fixture tests and historical locally built images remain separately identified evidence.

Readiness does not follow from having a manifest capability: runtime health must confirm it. State experimental limits in the notes, including unvalidated scale and other platforms, local-only development previews and unsupported per-task disk/I/O/network throttling. Do not imply that publishing a Guest also provisions a Linux host or changes macOS defaults.

### Draft, publish and verify the Guest

Reuse [tag and draft review](#prepare-the-tag-and-draft) with the approved Guest tag and source SHA, release immutability enabled, and `--draft --prerelease --latest=false`. Upload the four preserved core assets; review any required additional source materials explicitly. Application and Guest candidates each use `SHA256SUMS`, so keep them in their respective releases.

Select the exact numeric draft release ID and require an exact asset inventory. For **every** asset, record its numeric ID, `state=uploaded`, filename, size and digest; download by asset ID to a fresh directory, compare its bytes and SHA-256 against the original, and verify `SHA256SUMS`. Re-extract the downloaded archive, verify the internal checksums and disk/manifest association, and rehearse the [manual installation](startup.md#linux-guest-bundle-installation) with the reviewed host configuration. Recheck the draft inventory, notes and remote tag's peeled source SHA before deliberate publication.

After publication, confirm `draft=false` and `immutable=true`, verify the release and **each** original asset with `gh release verify` and `gh release verify-asset`, then download all public assets into a new directory and repeat byte/checksum/bundle checks. Keep availability pending on any failure. Never replace frozen assets or move a published tag; corrections need a new Guest version.

Update availability and versioned download links only after these checks pass. A later reviewed `artifacts.Official` entry must reference actual immutable disk URLs/digests and the full contract, and requires a consumer that supports its provider. Build provenance attestations remain a separate improvement; they do not replace these gates or block this manual first-publication path.
