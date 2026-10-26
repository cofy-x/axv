# AXV guest distribution

AXV's guest service source is MIT licensed (`LICENSE`). `build-info.json` binds these materials to the exact guest executable, image target, Go toolchain and linked modules. Its VCS settings are retained as recorded; collecting notices does not approve a build for publication.

`licenses/manifest.json` inventories only that executable's Go toolchain and linked Go modules. `licenses/` retains their original license, copyright, patent and notice files. It does not include the desktop's JavaScript dependencies or relicense upstream software as MIT.

`os-packages.tsv` records the image's dpkg package database, including package state, binary/source package names, versions and architecture; `os-release` records its OS identity. The `os-distribution/` directory retains these additional OS materials:

| File | Meaning |
| --- | --- |
| `apt-sources.json` | Original source configuration text, paths, byte counts and digests; credential-bearing sources are rejected, and authentication/proxy configuration and private keys are excluded |
| `apt-indexes.json` | Installation-time APT index paths, byte counts and digests, recorded before package-list cleanup |
| `notices.json`, `notices/` | Original OS copyright, license and notice files and common-license texts, with original paths and retained-file digests |
| `source-review.json` | Exact source package/version inventory and related binary packages; entries explicitly require review |
| `SHA256SUMS` | Checksums of the retained OS materials |

Ubuntu package copyright and license files also remain in the image under `/usr/share/doc`. The collector retains standard copyright, license and notice filenames; review the package sources for additional terms or notices under other names. These inventories describe the built image, not a promise of reproducible APT resolution or a complete legal assessment. Index hashes do not preserve package payloads or source archives.

The base image, OS packages and separately supplied kernel retain their own terms. Before redistribution, review [Canonical's intellectual property rights policy](https://ubuntu.com/legal/intellectual-property-policy) alongside the exact original package notices, including applicable redistribution and trademark terms. Verify source retrieval for every recorded source package/version, and provide the corresponding source, written offer or other materials required by the applicable terms. `source-review.json` is an unfinished review inventory; the collector neither downloads corresponding source nor certifies compliance. The application binary's separate license review does not cover this OS image. Keep this directory with the image bundle. The repository's [image recipes](https://github.com/cofy-x/axv/blob/main/images/guest/README.md) own publication and native acceptance requirements.
