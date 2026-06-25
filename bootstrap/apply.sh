#!/usr/bin/env bash
# Podplane <https://podplane.dev>
# Copyright The Podplane Authors
# SPDX-License-Identifier: Apache-2.0
#
# Bootstrap a Podplane components installation onto a Kubernetes cluster.
#
# Installs the minimum set of charts needed to take a bare cluster from
# `Nodes NotReady` to a Flux CD-managed Podplane components environment:
#
#   1. cilium-crds      - so cilium can install its CRs
#   2. cilium           - CNI; nodes go Ready after this
#   3. coredns          - cluster DNS at the well-known ClusterIP so Flux
#                         source-controller can resolve external Git hosts
#                         before it has cloned the components repo
#   4. fluxcd-crds      - so Flux can install GitRepository / HelmRelease CRs
#   5. fluxcd           - source-controller + helm-controller
#   6. platform-components - Flux-managed chart that reconciles all HelmReleases
#
# After this script, all further component installs / upgrades are reconciled
# by Flux from the `charts/platform-components` chart's values.

set -eo pipefail

REPO_DIR=$(cd "$(dirname "$(readlink -f "$0")")/.." && pwd)

# Required commands.
for cmd in helm kubectl; do
    if ! command -v "${cmd}" >/dev/null 2>&1; then
        echo "Error: required command '${cmd}' not found in PATH." >&2
        exit 1
    fi
done

step() {
    echo
    echo "==> [$1/6] $2"
}

set_bootstrap() {
    bootstrap_args+=(--set-string "$1=$2")
}

set_bootstrap_if_present() {
    if [[ -n "${2:-}" ]]; then
        set_bootstrap "$1" "$2"
    fi
}

bootstrap_args=(--namespace platform-components)
cilium_args=()
coredns_args=()
fluxcd_args=()

set_bootstrap_if_present bootstrap.install "${PLATFORM_INSTALL:-}"
set_bootstrap_if_present bootstrap.clusterID "${CLUSTER_ID:-}"
# DOMAIN wires platform-components values so Traefik ingress uses a real
# cluster domain. This matches what `podplane hooks netsy-seed` derives from
# cluster.domains. With PLATFORM_INSTALL=recommended or all, it makes a bare
# bootstrap usable without ACME credentials by using the platform self-signed
# ClusterIssuer.
set_bootstrap_if_present bootstrap.domain "${DOMAIN:-}"
set_bootstrap_if_present bootstrap.flux.git.url "${PLATFORM_GIT_REPOSITORY_URL:-}"
set_bootstrap_if_present bootstrap.flux.git.ref.branch "${PLATFORM_GIT_REPOSITORY_BRANCH:-}"
set_bootstrap_if_present bootstrap.flux.git.ref.tag "${PLATFORM_GIT_REPOSITORY_TAG:-}"
set_bootstrap_if_present bootstrap.flux.git.ref.commit "${PLATFORM_GIT_REPOSITORY_COMMIT:-}"
set_bootstrap_if_present bootstrap.flux.git.ref.semver "${PLATFORM_GIT_REPOSITORY_SEMVER:-}"

# Configure custom container registry/mirror.
if [[ -n "${REGISTRY_HOSTNAME:-}" ]]; then
    cilium_args+=(
        --set-string "cilium.image.repository=${REGISTRY_HOSTNAME}/quay.io/cilium/cilium"
        --set-string "cilium.operator.image.repository=${REGISTRY_HOSTNAME}/quay.io/cilium/operator"
    )
    coredns_args+=(
        --set-string "coredns.image.repository=${REGISTRY_HOSTNAME}/docker.io/coredns/coredns"
    )
    fluxcd_args+=(
        --set-string "flux2.sourceController.image=${REGISTRY_HOSTNAME}/ghcr.io/fluxcd/source-controller"
        --set-string "flux2.helmController.image=${REGISTRY_HOSTNAME}/ghcr.io/fluxcd/helm-controller"
        --set-string "flux2.cli.image=${REGISTRY_HOSTNAME}/ghcr.io/fluxcd/flux-cli"
    )
    set_bootstrap bootstrap.registry.hostname "${REGISTRY_HOSTNAME}"
fi

if [[ -n "${PLATFORM_GIT_CA_CERT_FILE:-}" ]]; then
    if [[ ! -f "${PLATFORM_GIT_CA_CERT_FILE}" ]]; then
        echo "Error: PLATFORM_GIT_CA_CERT_FILE does not exist: ${PLATFORM_GIT_CA_CERT_FILE}" >&2
        exit 1
    fi
    if [[ -z "${PLATFORM_GIT_SECRET_REF_NAME:-}" ]]; then
        PLATFORM_GIT_SECRET_REF_NAME="podplane-components-git"
    fi
    platform_git_ca_b64=$(base64 <"${PLATFORM_GIT_CA_CERT_FILE}" | tr -d '\n')
    set_bootstrap bootstrap.flux.git.caCert "${platform_git_ca_b64}"
fi
set_bootstrap_if_present bootstrap.flux.git.secretRefName "${PLATFORM_GIT_SECRET_REF_NAME:-}"

platform_manifest=$(mktemp)
trap 'rm -f "${platform_manifest}"' EXIT
helm template podplane-components-bootstrap "${REPO_DIR}/bootstrap" "${bootstrap_args[@]}" >"${platform_manifest}"

# Update chart dependencies so subcharts (cilium, coredns, flux2, etc.) are available.
echo "==> [0/6] helm dependency update"
helm dependency update "${REPO_DIR}/charts/cilium" >/dev/null
helm dependency update "${REPO_DIR}/charts/coredns" >/dev/null
helm dependency update "${REPO_DIR}/charts/fluxcd" >/dev/null

step 1 "Installing cilium CRDs (platform-cluster) ..."
helm upgrade --install platform-cilium-crds "${REPO_DIR}/charts/cilium-crds" \
    --namespace platform-cluster --create-namespace --wait

step 2 "Installing cilium (platform-cilium) -- waiting for Nodes to go Ready ..."
helm upgrade --install platform-cilium "${REPO_DIR}/charts/cilium" \
    --namespace platform-cilium --create-namespace --wait --skip-crds \
    "${cilium_args[@]}"

step 3 "Installing CoreDNS (platform-coredns) -- waiting for cluster DNS to be Ready ..."
helm upgrade --install platform-coredns "${REPO_DIR}/charts/coredns" \
    --namespace platform-coredns --create-namespace --wait --skip-crds \
    "${coredns_args[@]}"

step 4 "Installing Flux CRDs (platform-cluster) ..."
helm upgrade --install platform-fluxcd-crds "${REPO_DIR}/charts/fluxcd-crds" \
    --namespace platform-cluster --create-namespace --wait

step 5 "Installing Flux source-controller + helm-controller (platform-fluxcd) -- waiting for controllers to be Ready ..."
helm upgrade --install platform-fluxcd "${REPO_DIR}/charts/fluxcd" \
    --namespace platform-fluxcd --create-namespace --wait --skip-crds \
    "${fluxcd_args[@]}"

step 6 "Installing platform components (platform-components) via Flux CD..."
kubectl apply -f "${platform_manifest}"
echo "==> Waiting for podplane-components GitRepository to become Ready..."
kubectl wait --for=condition=Ready gitrepository/podplane-components --namespace platform-components --timeout=120s
echo "==> Waiting for platform-components HelmRelease to become Ready..."
if ! kubectl wait --for=condition=Ready helmrelease/platform-components --namespace platform-components --timeout=300s; then
    echo "Error: platform-components HelmRelease did not become Ready." >&2
    echo >&2
    kubectl get helmreleases -A >&2 || true
    echo >&2
    kubectl describe helmrelease/platform-components --namespace platform-components >&2 || true
    exit 1
fi

echo
echo "Bootstrap complete. Flux is now reconciling components."
echo "Watch reconciliation with:"
echo "  kubectl get helmreleases -A"
