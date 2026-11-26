# Releases and distribution

This page owns release preparation, version selection, multi-platform application and Guest candidate artifacts, build provenance and publication checks. [Development](development.md#verification) owns required CI checks; [acceptance](acceptance.md) owns native verification; [project status](../README.md#project-status) owns current availability. An Actions artifact is a candidate for review, not a published release.

## Version and source

- Use `major.minor.patch` with an optional prerelease suffix (`-rc.<n>` for applications, `-guest.<n>` for Guest images). Choose an unused `v<version>` tag after checking [Releases](https://github.com/cofy-x/axv/releases), remote tags and maintainer drafts. Never reuse a published tag or mix Guest assets into an application release.
- Keep the three private JS manifests aligned on the shared base version (`private: true` stays enabled). Supply the full `VERSION` to build, smoke, E2E and packaging; ordinary source builds report `dev`.
- Prepare releases only from a reviewed commit on `main` with passing required CI and a clean checkout. Confirm [project status](../README.md#project-status) claims and document breaking [AXV-store baseline changes](storage.md#desktop-database-baseline) in release notes.

## Build a candidate

The manual [Release candidate workflow](../.github/workflows/release.yml) builds and smoke-tests native **Linux amd64**, **Linux arm64** and **macOS arm64** executables, attests each platform candidate, then runs a `bundle` job that verifies all three, builds one combined `SHA256SUMS`, attests all three archives together with that combined `SHA256SUMS` in a single `provenance.json`, and uploads `axv-<version>-release-bundle`. It does not create tags, GitHub Releases or VM images.

- [`scripts/package-release.mjs`](../scripts/package-release.mjs) also supports local native packaging (including macOS amd64).
- [`scripts/render-homebrew-formula.mjs`](../scripts/render-homebrew-formula.mjs) renders a multi-platform Homebrew Formula from published checksums.

For local native packaging with the [pinned toolchain](../README.md#install-and-use):

```sh
axv_candidate_version='<unused-application-version>'
make deps
VERSION="$axv_candidate_version" node scripts/package-release.mjs --check-version
VERSION="$axv_candidate_version" make build
VERSION="$axv_candidate_version" make smoke-production
VERSION="$axv_candidate_version" node scripts/package-release.mjs
```

The packager verifies the binary's version, production build tags (`production,ts_omit_webclient`), target, `CGO_ENABLED=0`, pinned Go version and clean VCS metadata (`vcs.modified=false`), writes `dist/release/<version>/` (refusing to overwrite an existing candidate directory), and checks checkout cleanliness again afterward.

| Bundle output | Contents |
| --- | --- |
| `axv_<version>_<os>_<arch>.tar.gz` | Executable with embedded UI, MIT license, third-party policy, collected dependency licenses, dependency inventory, `build-info.json` and startup pointers (`linux_amd64`, `linux_arm64`, `darwin_arm64`) |
| `SHA256SUMS` | Combined SHA-256 manifest of the release archives |
| `provenance.json` | Original GitHub Sigstore build-provenance bundle covering the archives and combined `SHA256SUMS` |

Archives bundle neither Node, VM providers, kernels, Codex nor credentials. Local packaging does not issue GitHub attestations; platform code signing and notarization remain unavailable.

## Verify build provenance

Both candidate workflows run SHA-pinned [`actions/attest` v4.2.2](https://github.com/actions/attest/tree/1e69f48acb82d1966a394da916b4c1698aa569d6) after final assembly (requiring `cofy-x/axv` and `refs/heads/main`; PR CI has no issuance permissions).

| Producing workflow | Attested subjects |
| --- | --- |
| `.github/workflows/release.yml` | `axv_<version>_<os>_<arch>.tar.gz` (`linux_amd64`, `linux_arm64`, `darwin_arm64`) and combined `SHA256SUMS` |
| `.github/workflows/guest-images.yml` | `axv_guest_<version>_firecracker_amd64.tar.gz`/`.ext4` or `axv_guest_<version>_lima_arm64.tar.gz`/`.qcow2`, `candidate.json`, `SHA256SUMS` |

`provenance.json` is neither its own subject nor listed in `SHA256SUMS`. Source companion tarballs are outside these subjects and reviewed separately.

Set `SOURCE_SHA` independently from the reviewed candidate run's full `head_sha` (not from the bundle or current `main`), and run [`scripts/verify-release-provenance.mjs`](../scripts/verify-release-provenance.mjs) with the pinned Node toolchain and GitHub CLI (`2.102.0` validated):

```sh
# Complete multi-platform application bundle (all 3 archives + combined SHA256SUMS):
node scripts/verify-release-provenance.mjs --kind app --version "$VERSION" \
  --all-targets --source "$SOURCE_SHA" --directory "$DOWNLOAD_DIRECTORY"

# Single platform archive + combined SHA256SUMS:
node scripts/verify-release-provenance.mjs --kind app --version "$VERSION" \
  --os linux --arch amd64 --source "$SOURCE_SHA" --directory "$DOWNLOAD_DIRECTORY"

# Guest candidates (Firecracker amd64 or Lima arm64):
node scripts/verify-release-provenance.mjs --kind guest --version "$VERSION" \
  --source "$SOURCE_SHA" --directory "$DOWNLOAD_DIRECTORY"
node scripts/verify-release-provenance.mjs --kind guest --provider lima --arch arm64 \
  --version "$VERSION" --source "$SOURCE_SHA" --directory "$DOWNLOAD_DIRECTORY"
```

The helper enforces `--repo cofy-x/axv`, the exact signer workflow, `--signer-digest` / `--source-digest "$SOURCE_SHA"`, `--source-ref refs/heads/main`, SLSA provenance v1, `--deny-self-hosted-runners`, and certificate `sourceRepositoryIdentifier == "1424477361"`. Build provenance proves build origin and integrity; it does not replace source review, license review, native acceptance or code signing.

## License review

[THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) owns distribution boundaries. [`scripts/release-notices.mjs`](../scripts/release-notices.mjs) inspects linked Go modules from the built binary, verifies cached sources via `go mod verify`, collects Go stdlib notices, dashboard production JS dependencies (`pnpm --filter-prod '{./apps/dashboard}...' licenses list --prod --json --recursive`), Vite/Rolldown helper notices and supplemental embedded-asset notices, and rejects any binary still linking `github.com/tailscale/web-client-prebuilt`.

Review `licenses/manifest.json` and original license texts before publication; missing license files or checksum mismatches abort packaging.

## Review and publish

Publication is an explicit maintainer action after candidate review. Keep approved originals untouched (never rebuild, recompress, edit or re-attest during promotion).

### Preserve and approve the candidate

1. Verify the tag is unused, record the candidate run's `head_sha`, and confirm required CI passed for that exact SHA.
2. Download the candidate bundle before Actions artifact expiry; extract the outer Actions ZIP wrapper and preserve the inner archives, `SHA256SUMS` and `provenance.json` untouched.
3. Run `--all-targets` [provenance verification](#verify-build-provenance) and checksum checks, then test [archive installation](startup.md#install-a-release-archive) in a fresh directory and isolated `AXV_HOME` (`axv version`, foreground startup, and native supervision/VM/agent checks for capabilities claimed in notes).
4. Record source SHA, candidate run/artifact IDs, exact filenames/sizes/SHA-256 digests (including `SHA256SUMS` and `provenance.json`), provenance verification, license review and acceptance evidence in the release PR/notes.

### Prepare the tag, draft and publish

Ensure repository [release immutability](https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/establish-provenance-and-integrity/prevent-release-changes) is enabled **before creating the draft**. Create and push an annotated tag at the exact `SOURCE_SHA`, then verify `^{}` peels to `SOURCE_SHA`:

```sh
git tag -a "$TAG" "$SOURCE_SHA" -m "AXV $TAG"
git push origin "refs/tags/$TAG"
git ls-remote origin "refs/tags/$TAG" "refs/tags/$TAG^{}"
```

Upload all preserved archives, `SHA256SUMS` and `provenance.json` as a draft prerelease (each file must be strictly `< 2 GiB`):

```sh
gh release create "$TAG" "${ARCHIVES[@]}" "$SUMS" "$PROVENANCE" \
  --repo cofy-x/axv --verify-tag --draft --prerelease --latest=false \
  --notes-file "$NOTES"
```

Before publishing:

- List releases (including drafts) via `gh api --paginate repos/cofy-x/axv/releases` and inspect the exact numeric release ID to confirm `draft=true`, `prerelease=true`, `state=uploaded` and exact asset inventory/sizes/digests.
- Prefer `gh release edit "$TAG" --notes-file "$NOTES"` when editing draft notes (raw REST `PATCH` without `tag_name` can clear the tag).
- Download every draft asset by ID (`gh api --allow-escape-sequences -H 'Accept: application/octet-stream' "repos/cofy-x/axv/releases/assets/$ASSET_ID" > "$FILE"`), verify byte equality (`cmp`), provenance, checksums and archive smoke, and re-verify the peeled remote tag.

Publish explicitly (include `--tag "$TAG"` so `gh` enforces `--verify-tag`):

```sh
gh release edit "$TAG" --repo cofy-x/axv --tag "$TAG" --verify-tag \
  --draft=false --prerelease --latest=false
```

After publication, confirm `draft=false` and `immutable=true`, run `gh release verify "$TAG" --repo cofy-x/axv` and `gh release verify-asset` for every uploaded file, download all public assets into a fresh directory and re-verify `cmp`, provenance, checksums and smoke before updating links. Never move a published tag or replace immutable assets; issue a new version for fixes.

## VM image publication

Guest images have independent versions and releases. [Image recipes](../images/guest/README.md#per-machine-initialization-and-release-work) own generated guest notices, OS package inventories and identity sanitization.

### Build and retain the Guest candidate

[`.github/workflows/guest-images.yml`](../.github/workflows/guest-images.yml) builds either **`firecracker-amd64`** (from a reviewed Ubuntu 24.04 OCI digest) or **`lima-arm64`** (from a reviewed Ubuntu 24.04 arm64 cloud image URL and SHA-256), runs [`scripts/package-guest-release.mjs`](../scripts/package-guest-release.mjs), attests the four core assets into `provenance.json` and uploads the candidate (14-day retention).

For local packaging of an image-builder output (add `--local` when testing uncommitted changes; outputs go to `.data/test/guest-candidate-*` marked `localOnly`):

```sh
axv_guest_release_version='<unused-guest-version>'
VERSION="$axv_guest_release_version" node scripts/package-guest-release.mjs --bundle /absolute/new-guest-bundle
```

| Guest asset | Contents |
| --- | --- |
| `axv_guest_<version>_<provider>_<arch>.tar.gz` | Complete disk, `manifest.json`, base identity, bundle checksums and `distribution/` |
| `axv_guest_<version>_<provider>_<arch>.<ext4\|qcow2>` | Byte-identical standalone disk (`< 2 GiB`; Firecracker uses 2047 MiB raw ext4, Lima uses compressed qcow2) |
| `candidate.json` | Exact image contract, base identity, archive/disk names and VCS/build metadata |
| `SHA256SUMS` | SHA-256 of the three assets above |
| `provenance.json` | Original build-provenance bundle covering the four core assets |

### Review and publish the Guest

Apply the same candidate preservation, provenance verification (`--kind guest` or `--kind guest --provider lima --arch arm64`), draft asset verification and post-publication checks to all five Guest files. Before approving a Guest draft:

1. Confirm clean VCS/build metadata, base digest, image contract **1**, guest protocol **3**, declared capabilities and guest executable digest.
2. Complete license and corresponding-source review for both Go notices and `distribution/axv-guest/os-distribution/` (resolving `reviewRequired` entries and providing any legally required source companion materials).
3. Directly inspect the disk image for cleared machine IDs and global SSH host keys, with no AXV machine config/token, dedicated SSH keys/clients or initialized task state.
4. Run native acceptance against the exact downloaded candidate and approved application binary ([Linux Firecracker](acceptance.md#linux-firecracker) or [macOS Lima](acceptance.md#macos-lima)) covering lifecycle, enhanced Guest/SSH, project files, persistent tasks, initialization, previews and backups as applicable.

Updating the default Linux installer pin in [`apps/server/internal/compute/artifacts/firecracker.go`](../apps/server/internal/compute/artifacts/firecracker.go) is a separate reviewed application change after Guest publication.
