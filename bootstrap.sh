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

REPO_DIR=$(cd "$(dirname "$(readlink -f "$0")")" && pwd)

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

# Update chart dependencies so subcharts (cilium, coredns, flux2, etc.) are available.
echo "==> [0/6] helm dependency update"
helm dependency update "${REPO_DIR}/charts/cilium" >/dev/null
helm dependency update "${REPO_DIR}/charts/coredns" >/dev/null
helm dependency update "${REPO_DIR}/charts/fluxcd" >/dev/null

platform_cluster_id="${CLUSTER_ID:-default}"
platform_git_secret=""
platform_git_secret_ref=""
platform_git_ref=""
platform_registry=""
platform_crds=""
platform_apps=""
platform_values=""
cilium_args=()
coredns_args=()
fluxcd_args=()

# DOMAIN, when set, wires the platform-components values so Traefik ingress
# uses a real cluster domain. This matches what `podplane hooks netsy-seed`
# derives from cluster.domains and is what makes `make recommended DOMAIN=...`
# produce a usable cluster straight out of bare bootstrap. The selfsigned
# ClusterIssuer is shipped by the platform-certs chart so it works without
# ACME credentials.
if [[ -n "${DOMAIN:-}" ]]; then
    platform_values+=$(cat <<YAML
          traefik:
            platform:
              traefik:
                ingress:
                  enabled: true
                  issuerRef:
                    kind: ClusterIssuer
                    name: platform-ingress-selfsigned-clusterissuer
                  domains:
                    - zone: ${DOMAIN}
                      default: true
YAML
)
    platform_values+=$'\n'
fi

# Configure custom container registry/mirror
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
    platform_registry+=$(cat <<YAML
          mirror:
            enabled: true
            hostname: ${REGISTRY_HOSTNAME}
YAML
)
    platform_registry+=$'\n'
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
    platform_git_secret+=$(cat <<YAML
apiVersion: v1
kind: Secret
metadata:
  name: ${PLATFORM_GIT_SECRET_REF_NAME}
  namespace: platform-components
type: Opaque
data:
  ca.crt: ${platform_git_ca_b64}
---
YAML
)
fi

if [[ -n "${PLATFORM_GIT_SECRET_REF_NAME:-}" ]]; then
    platform_git_secret_ref+=$(cat <<YAML
  secretRef:
    name: ${PLATFORM_GIT_SECRET_REF_NAME}
YAML
)
fi

if [[ -n "${PLATFORM_GIT_REPOSITORY_BRANCH:-}" ]]; then
    platform_git_ref="    branch: \"${PLATFORM_GIT_REPOSITORY_BRANCH}\""
elif [[ -n "${PLATFORM_GIT_REPOSITORY_TAG:-}" ]]; then
    platform_git_ref="    tag: \"${PLATFORM_GIT_REPOSITORY_TAG}\""
elif [[ -n "${PLATFORM_GIT_REPOSITORY_COMMIT:-}" ]]; then
    platform_git_ref="    commit: \"${PLATFORM_GIT_REPOSITORY_COMMIT}\""
else
    platform_git_ref="    semver: \"${PLATFORM_GIT_REPOSITORY_SEMVER:->=1.0.0}\""
fi

# PLATFORM_INSTALL selects the platform-components CRD & app installation set:
#   minimal     - core only (default for this script)
#   recommended - core + curated addons (agent-sandbox, cert-manager, trust-manager, traefik, podplane-operator, secrets-store-csi-driver, OpenBao secrets provider)
#   all         - every app & CRD in this repo
case "${PLATFORM_INSTALL:-minimal}" in
    minimal)
        ;;
    recommended)
        platform_crds+=$(cat <<'YAML'
          agent-sandbox-crds:
            enabled: true
          cert-manager-crds:
            enabled: true
          podplane-operator-crds:
            enabled: true
          secrets-store-csi-driver-crds:
            enabled: true
          trust-manager-crds:
            enabled: true
          traefik-crds:
            enabled: true
YAML
)
        platform_crds+=$'\n'
        platform_apps+=$(cat <<'YAML'
          agent-sandbox:
            enabled: true
          cert-manager:
            enabled: true
          platform-certs:
            enabled: true
          podplane-operator:
            enabled: true
          secrets-store-csi-driver:
            enabled: true
          secrets-store-csi-provider-openbao:
            enabled: true
          trust-manager:
            enabled: true
          platform-trust:
            enabled: true
          traefik:
            enabled: true
YAML
)
        platform_apps+=$'\n'
        ;;
    all)
        platform_crds+=$(cat <<'YAML'
          agent-sandbox-crds:
            enabled: true
          cert-manager-crds:
            enabled: true
          podplane-operator-crds:
            enabled: true
          secrets-store-csi-driver-crds:
            enabled: true
          trust-manager-crds:
            enabled: true
          traefik-crds:
            enabled: true
          snapshot-crds:
            enabled: true
YAML
)
        platform_crds+=$'\n'
        platform_apps+=$(cat <<'YAML'
          agent-sandbox:
            enabled: true
          cert-manager:
            enabled: true
          platform-certs:
            enabled: true
          podplane-operator:
            enabled: true
          secrets-store-csi-driver:
            enabled: true
          secrets-store-csi-provider-aws:
            enabled: true
          secrets-store-csi-provider-gcp:
            enabled: true
          secrets-store-csi-provider-vault:
            enabled: true
          secrets-store-csi-provider-openbao:
            enabled: true
          trust-manager:
            enabled: true
          platform-trust:
            enabled: true
          traefik:
            enabled: true
          snapshot:
            enabled: true
YAML
)
        platform_apps+=$'\n'
        ;;
    *)
        echo "Error: PLATFORM_INSTALL value must be minimal, recommended, or all." >&2
        exit 1
        ;;
esac

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
platform_manifest=$(mktemp)
trap 'rm -f "${platform_manifest}"' EXIT
cat >"${platform_manifest}" <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: platform-components
---
${platform_git_secret}
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: podplane-components
  namespace: platform-components
spec:
  interval: 10m
  url: "${PLATFORM_GIT_REPOSITORY_URL:-https://github.com/podplane/components.git}"
${platform_git_secret_ref}
  ref:
${platform_git_ref}
---
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: platform-components
  namespace: platform-components
spec:
  interval: 10m
  maxHistory: 3
  releaseName: platform-components
  chart:
    spec:
      chart: ./charts/platform-components
      sourceRef:
        kind: GitRepository
        name: podplane-components
        namespace: platform-components
YAML
cat >>"${platform_manifest}" <<YAML
  values:
    platform:
      components:
        clusterID: "${platform_cluster_id}"
YAML
if [[ -n "${platform_registry}" ]]; then
    cat >>"${platform_manifest}" <<YAML
        registry:
${platform_registry}
YAML
fi
if [[ -n "${platform_crds}" ]]; then
    cat >>"${platform_manifest}" <<YAML
        crds:
${platform_crds}
YAML
fi
if [[ -n "${platform_apps}" ]]; then
    cat >>"${platform_manifest}" <<YAML
        apps:
${platform_apps}
YAML
fi
if [[ -n "${platform_values}" ]]; then
    cat >>"${platform_manifest}" <<YAML
        values:
${platform_values}
YAML
fi
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
