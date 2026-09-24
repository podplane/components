#!/usr/bin/env bash

# Podplane <https://podplane.dev>
# Copyright The Podplane Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <envoy-gateway-version>" >&2
  exit 2
fi

for command in git helm patch rsync; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "$command is required" >&2
    exit 1
  }
done

version="$1"
case "$version" in
  v*) ;;
  *) version="v$version" ;;
esac

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
chart_dir="$(cd "$script_dir/.." && pwd)"
repo_root="$(cd "$chart_dir/../.." && pwd)"
source_dir="$chart_dir/upstream/gateway-helm"
patch_file="$chart_dir/patches/gateway-helm.patch"
repository="https://github.com/envoyproxy/gateway"
removed_files=(
  templates/certgen-rbac.yaml
  templates/certgen.yaml
)

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading Envoy Gateway $version..."
helm pull oci://docker.io/envoyproxy/gateway-helm \
  --version "$version" \
  --untar \
  --untardir "$tmp"

for file in "${removed_files[@]}"; do
  rm "$tmp/gateway-helm/$file"
done

echo "Applying Podplane patches..."
patch --batch --forward --strip=2 --directory="$tmp/gateway-helm" < "$patch_file"

tag_ref="refs/tags/$version"
refs="$(git ls-remote "$repository.git" "$tag_ref" "$tag_ref^{}")"
commit="$(printf '%s\n' "$refs" | awk -v ref="$tag_ref^{}" '$2 == ref { print $1; exit }')"
if [ -z "$commit" ]; then
  commit="$(printf '%s\n' "$refs" | awk -v ref="$tag_ref" '$2 == ref { print $1; exit }')"
fi
if [ -z "$commit" ]; then
  echo "could not resolve Git commit for $version" >&2
  exit 1
fi

echo "Replacing vendored chart source..."
mkdir -p "$source_dir"
rsync -a --delete "$tmp/gateway-helm/" "$source_dir/"

app_version="${version#v}"
chart_yaml_tmp="$chart_dir/Chart.yaml.tmp"
awk -v app_version="$app_version" -v dependency_version="$version" '
  /^appVersion:/ {
    print "appVersion: \"" app_version "\""
    next
  }
  /^dependencies:/ {
    dependencies = 1
  }
  dependencies && /^    version:/ {
    print "    version: \"" dependency_version "\""
    dependencies = 0
    next
  }
  { print }
' "$chart_dir/Chart.yaml" > "$chart_yaml_tmp"
mv "$chart_yaml_tmp" "$chart_dir/Chart.yaml"

cat > "$chart_dir/SOURCE.yaml" <<EOF
repository: $repository
chart: charts/gateway-helm
version: $version
commit: $commit
EOF

echo "Refreshing Helm dependency..."
helm dependency update --skip-refresh "$chart_dir"

echo "Validating Envoy Gateway chart..."
helm lint --kube-version 1.37.0 "$chart_dir"
(
  cd "$repo_root"
  go test ./tests -run TestEnvoyGateway -count=1
)

echo "Updated Envoy Gateway chart to $version ($commit)."
