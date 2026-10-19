#!/bin/sh
set -eu
# Run in the image after dependency installation, never against a host database.
if [ "$#" != 1 ] || [ ! -d "$1" ]; then
  echo 'Usage: package-inventory.sh /existing/distribution-directory' >&2
  exit 1
fi
export LC_ALL=C
inventory=$(mktemp "$1/.packages-XXXXXX")
trap 'rm -f -- "$inventory"' EXIT HUP INT TERM
# Keep dpkg's status field rather than silently omitting held or residual entries.
dpkg-query --show --showformat='${db:Status-Abbrev}\t${binary:Package}\t${Version}\t${Architecture}\t${source:Package}\t${source:Version}\n' > "$inventory"
test -s "$inventory"
{
  printf 'Status\tBinary-Package\tVersion\tArchitecture\tSource-Package\tSource-Version\n'
  sort "$inventory"
} > "$1/os-packages.tsv"
cat /etc/os-release > "$1/os-release"
