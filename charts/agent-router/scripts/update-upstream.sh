#!/usr/bin/env bash

# Podplane <https://podplane.dev>
# Copyright The Podplane Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <agent-router-version>" >&2
  exit 2
fi

version="v${1#v}"
chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

helm pull oci://docker.io/envoyproxy/ai-gateway-helm \
  --version "$version" --untar --untardir "$tmp"
patch --batch --forward --no-backup-if-mismatch --strip=2 --directory="$tmp/ai-gateway-helm" < "$chart_dir/patches/ai-gateway-helm.patch"

repository="https://github.com/theagentrouter/agent-router"
refs="$(git ls-remote "$repository.git" "refs/tags/$version" "refs/tags/$version^{}")"
commit="$(printf '%s\n' "$refs" | awk '$2 ~ /\^\{\}$/ { print $1; exit }')"
if [ -z "$commit" ]; then
  commit="$(printf '%s\n' "$refs" | awk 'NR == 1 { print $1 }')"
fi
test -n "$commit"

mkdir -p "$chart_dir/upstream/ai-gateway-helm"
rsync -a --delete "$tmp/ai-gateway-helm/" "$chart_dir/upstream/ai-gateway-helm/"
sed -E "s/^appVersion:.*/appVersion: \"$version\"/; s/^    version:.*/    version: \"$version\"/" \
  "$chart_dir/Chart.yaml" > "$tmp/Chart.yaml"
mv "$tmp/Chart.yaml" "$chart_dir/Chart.yaml"
cat > "$chart_dir/SOURCE.yaml" <<EOF
repository: $repository
chart: manifests/charts/ai-gateway-helm
version: $version
commit: $commit
EOF
helm dependency update --skip-refresh "$chart_dir"
helm lint --kube-version 1.37.0 "$chart_dir"
