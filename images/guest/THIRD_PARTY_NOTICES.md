# AXV guest distribution

AXV's guest service source is MIT licensed (`LICENSE`). `build-info.json` binds these materials to the exact guest executable, image target, Go toolchain and linked modules. Its VCS settings are retained as recorded; collecting notices does not approve a build for publication.

`licenses/manifest.json` inventories only that executable's Go toolchain and linked Go modules. `licenses/` retains their original license, copyright, patent and notice files. It does not include the desktop's JavaScript dependencies or relicense upstream software as MIT.

`os-packages.tsv` records the image's dpkg package database, including package state, binary/source package names, versions and architecture; `os-release` records its OS identity. Ubuntu package copyright and license files remain in the image under `/usr/share/doc`. These inventories describe the built image, not a promise of reproducible APT resolution or a complete legal assessment.

The base image, OS packages and separately supplied kernel retain their own terms. Review the exact originals and applicable corresponding-source requirements before redistribution. Keep this directory with the image bundle. The repository's [image recipes](https://github.com/cofy-x/axv/blob/main/images/guest/README.md) own publication and native acceptance requirements.
