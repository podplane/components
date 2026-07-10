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
    echo "==> [$1/9] $2"
}

set_bootstrap_args() {
    if [[ -n "${2:-}" ]]; then
        bootstrap_args+=(--set-string "$1=$2")
    fi
}

set_bootstrap_bool() {
    if [[ -n "${2:-}" ]]; then
        bootstrap_args+=(--set "$1=$2")
    fi
}

RESOLVED_REGISTRY="${REGISTRY_HOSTNAME:+${REGISTRY_HOSTNAME}/mirror/}"

bootstrap_args=(--namespace platform-components)
set_bootstrap_args bootstrap.install "${PLATFORM_INSTALL:-}"
set_bootstrap_args bootstrap.clusterID "${CLUSTER_ID:-}"
# DOMAIN wires platform-components values so Traefik ingress uses a real
# cluster domain. This matches what `podplane hooks netsy-seed` derives from
# cluster.domains. With PLATFORM_INSTALL=recommended or all, it makes a bare
# bootstrap usable without ACME credentials by using the platform self-signed
# ClusterIssuer.
set_bootstrap_args bootstrap.domain "${DOMAIN:-}"
set_bootstrap_args bootstrap.flux.git.url "${PLATFORM_GIT_REPOSITORY_URL:-}"
set_bootstrap_args bootstrap.flux.git.ref.branch "${PLATFORM_GIT_REPOSITORY_BRANCH:-}"
set_bootstrap_args bootstrap.flux.git.ref.tag "${PLATFORM_GIT_REPOSITORY_TAG:-}"
set_bootstrap_args bootstrap.flux.git.ref.commit "${PLATFORM_GIT_REPOSITORY_COMMIT:-}"
set_bootstrap_args bootstrap.flux.git.ref.semver "${PLATFORM_GIT_REPOSITORY_SEMVER:-}"
set_bootstrap_args bootstrap.registry.hostname "${REGISTRY_HOSTNAME:-}"
set_bootstrap_args bootstrap.registry.bucket "${REGISTRY_BUCKET:-}"
set_bootstrap_args bootstrap.registry.region "${REGISTRY_REGION:-}"
set_bootstrap_args bootstrap.registry.endpoint "${REGISTRY_ENDPOINT:-}"
set_bootstrap_bool bootstrap.registry.forcePathStyle "${AWS_S3_USE_PATH_STYLE:-}"
if [[ "${REGISTRY_ENDPOINT:-}" == http://* ]]; then
    set_bootstrap_bool bootstrap.registry.secure false
fi
set_bootstrap_args bootstrap.registry.accessKeyID "${REGISTRY_ACCESS_KEY_ID:-}"
set_bootstrap_args bootstrap.registry.secretAccessKey "${REGISTRY_SECRET_ACCESS_KEY:-}"
set_bootstrap_args bootstrap.oidc.issuer "${OIDC_ISSUER:-}"
set_bootstrap_args bootstrap.oidc.audience "${OIDC_AUDIENCE:-${CLUSTER_ID:-}}"
set_bootstrap_args bootstrap.oidc.usernameClaim "${OIDC_USERNAME_CLAIM:-}"
set_bootstrap_args bootstrap.oidc.groupsClaim "${OIDC_GROUPS_CLAIM:-}"
set_bootstrap_args bootstrap.secrets.localFakeVault.address "${LOCAL_FAKEVAULT_ADDRESS:-}"

if [[ -n "${LOCAL_FAKEVAULT_CA_CERT_FILE:-}" ]]; then
    if [[ ! -f "${LOCAL_FAKEVAULT_CA_CERT_FILE}" ]]; then
        echo "Error: LOCAL_FAKEVAULT_CA_CERT_FILE does not exist: ${LOCAL_FAKEVAULT_CA_CERT_FILE}" >&2
        exit 1
    fi
    local_fakevault_ca_b64=$(base64 <"${LOCAL_FAKEVAULT_CA_CERT_FILE}" | tr -d '\n')
    set_bootstrap_args bootstrap.secrets.localFakeVault.caCert "${local_fakevault_ca_b64}"
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
    set_bootstrap_args bootstrap.flux.git.caCert "${platform_git_ca_b64}"
fi
set_bootstrap_args bootstrap.flux.git.secretRefName "${PLATFORM_GIT_SECRET_REF_NAME:-}"

platform_manifest=$(mktemp)
trap 'rm -f "${platform_manifest}"' EXIT
helm template podplane-components-bootstrap "${REPO_DIR}/bootstrap" "${bootstrap_args[@]}" >"${platform_manifest}"

step 1 "Updating Helm chart dependencies ..."
helm dependency update "${REPO_DIR}/charts/cilium" >/dev/null
helm dependency update "${REPO_DIR}/charts/coredns" >/dev/null
helm dependency update "${REPO_DIR}/charts/fluxcd" >/dev/null

step 2 "Installing cilium CRDs (platform-cluster) ..."
helm upgrade --install platform-cilium-crds "${REPO_DIR}/charts/cilium-crds" \
    --namespace platform-cluster --create-namespace --wait

step 3 "Installing cilium (platform-cilium) -- waiting for Nodes to go Ready ..."
helm upgrade --install platform-cilium "${REPO_DIR}/charts/cilium" \
    --namespace platform-cilium --create-namespace --wait --skip-crds \
    --set-string "cilium.image.repository=${RESOLVED_REGISTRY}quay.io/cilium/cilium" \
    --set-string "cilium.operator.image.repository=${RESOLVED_REGISTRY}quay.io/cilium/operator"

step 4 "Installing CoreDNS (platform-coredns) -- waiting for cluster DNS to be Ready ..."
helm upgrade --install platform-coredns "${REPO_DIR}/charts/coredns" \
    --namespace platform-coredns --create-namespace --wait --skip-crds \
    --set-string "coredns.image.repository=${RESOLVED_REGISTRY}docker.io/coredns/coredns"

step 5 "Installing Flux CRDs (platform-cluster) ..."
helm upgrade --install platform-fluxcd-crds "${REPO_DIR}/charts/fluxcd-crds" \
    --namespace platform-cluster --create-namespace --wait

step 6 "Installing Flux source-controller + helm-controller (platform-fluxcd) -- waiting for controllers to be Ready ..."
helm upgrade --install platform-fluxcd "${REPO_DIR}/charts/fluxcd" \
    --namespace platform-fluxcd --create-namespace --wait --skip-crds \
    --set-string "flux2.sourceController.image=${RESOLVED_REGISTRY}ghcr.io/fluxcd/source-controller" \
    --set-string "flux2.helmController.image=${RESOLVED_REGISTRY}ghcr.io/fluxcd/helm-controller" \
    --set-string "flux2.cli.image=${RESOLVED_REGISTRY}ghcr.io/fluxcd/flux-cli"

step 7 "Installing platform components (platform-components) via Flux CD..."
kubectl apply -f "${platform_manifest}"

step 8 "Waiting for podplane-components GitRepository to become Ready..."
kubectl wait --for=condition=Ready gitrepository/podplane-components --namespace platform-components --timeout=120s

step 9 "Waiting for platform-components HelmRelease to become Ready..."
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
