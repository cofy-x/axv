#!/bin/sh
set -eu
# Run in the image after dependency installation, never against a host database.
if [ "$#" != 1 ] || [ ! -d "$1" ]; then
  echo 'Usage: package-inventory.sh /existing/distribution-directory' >&2
  exit 1
fi
exec python3 "$(dirname -- "$0")/package-inventory.py" "$1"
