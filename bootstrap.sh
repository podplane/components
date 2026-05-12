#!/usr/bin/env bash
# Podplane <https://podplane.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0
#
# Bootstrap a Podplane components installation onto a Kubernetes cluster.
#
# Installs the minimum set of charts needed to take a bare cluster from
# `Nodes NotReady` to a Flux CD-managed Podplane components environment:
#
#   1. cilium-crds      - so cilium can install its CRs
#   2. cilium           - CNI; nodes go Ready after this
#   3. fluxcd-crds      - so Flux can install GitRepository / HelmRelease CRs
#   4. fluxcd           - source-controller + helm-controller
#   5. platform-components - Flux-managed chart that reconciles all HelmReleases
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
    echo "==> [$1/5] $2"
}

deploy_kind_git_server() {
    kubectl apply -f - <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: components-git
  namespace: platform-fluxcd
  labels:
    app.kubernetes.io/name: components-git
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: components-git
  template:
    metadata:
      labels:
        app.kubernetes.io/name: components-git
    spec:
      containers:
        - name: git-http
          image: caddy:2-alpine
          command:
            - /bin/sh
            - -ec
          args:
            - |
              apk add --no-cache git-daemon fcgiwrap spawn-fcgi
              git_http_backend=$(command -v git-http-backend || find /usr -path '*/git-http-backend' -type f | head -n1)
              test -n "${git_http_backend}"
              cat >/etc/caddy/Caddyfile <<'CADDY'
              :80 {
                reverse_proxy unix//run/fcgiwrap.sock {
                  transport fastcgi {
                    env SCRIPT_FILENAME __GIT_HTTP_BACKEND__
                    env GIT_PROJECT_ROOT /git
                    env GIT_HTTP_EXPORT_ALL 1
                    env PATH_INFO {path}
                    env QUERY_STRING {query}
                    env REQUEST_METHOD {method}
                    env CONTENT_TYPE {header.Content-Type}
                    env CONTENT_LENGTH {header.Content-Length}
                  }
                }
              }
              CADDY
              sed -i "s#__GIT_HTTP_BACKEND__#${git_http_backend}#" /etc/caddy/Caddyfile
              spawn-fcgi -s /run/fcgiwrap.sock -M 766 /usr/bin/fcgiwrap
              exec caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
          ports:
            - name: http
              containerPort: 80
          volumeMounts:
            - name: git-repos
              mountPath: /git
              readOnly: true
      volumes:
        - name: git-repos
          hostPath:
            path: /podplane-components-kind-git
            type: Directory
---
apiVersion: v1
kind: Service
metadata:
  name: components-git
  namespace: platform-fluxcd
  labels:
    app.kubernetes.io/name: components-git
spec:
  selector:
    app.kubernetes.io/name: components-git
  ports:
    - name: http
      port: 80
      targetPort: http
YAML
}

# Update chart dependencies so subcharts (cilium, flux2, etc.) are available.
echo "==> [0/5] helm dependency update"
helm dependency update "${REPO_DIR}/charts/cilium" >/dev/null
helm dependency update "${REPO_DIR}/charts/fluxcd" >/dev/null

cilium_args=()
fluxcd_args=()
platform_values=""

# Override the Cilium Kubernetes API server hostname config param, set when using kind
if [[ -n "${CILIUM_K8S_SERVICE_HOST:-}" ]]; then
    cilium_args+=(--set-string "cilium.k8sServiceHost=${CILIUM_K8S_SERVICE_HOST}")
    # We also need to mirror the override into the platform-components values file,
    # so when Flux takes over the cilium HelmRelease it doesn't re-render with the
    # chart defaults
    platform_values+="        valuesObject:"$'\n'
    platform_values+="          cilium:"$'\n'
    platform_values+="            cilium:"$'\n'
    platform_values+="              k8sServiceHost: ${CILIUM_K8S_SERVICE_HOST}"$'\n'
fi

# Configure custom container registry/mirror
if [[ -n "${REGISTRY_HOSTNAME:-}" ]]; then
    cilium_args+=(
        --set-string "cilium.image.repository=${REGISTRY_HOSTNAME}/quay.io/cilium/cilium"
        --set-string "cilium.operator.image.repository=${REGISTRY_HOSTNAME}/quay.io/cilium/operator"
    )
    fluxcd_args+=(
        --set-string "flux2.sourceController.image=${REGISTRY_HOSTNAME}/ghcr.io/fluxcd/source-controller"
        --set-string "flux2.helmController.image=${REGISTRY_HOSTNAME}/ghcr.io/fluxcd/helm-controller"
        --set-string "flux2.cli.image=${REGISTRY_HOSTNAME}/ghcr.io/fluxcd/flux-cli"
    )
    platform_values+=$(cat <<YAML
        registry:
          mirror:
            enabled: true
            hostname: ${REGISTRY_HOSTNAME}
YAML
)
fi

case "${PLATFORM_INSTALL_ALL:-0}" in
    0)
        ;;
    1)
        platform_values+=$(cat <<'YAML'
        crds:
          cert-manager-crds:
            enabled: true
          trust-manager-crds:
            enabled: true
          traefik-crds:
            enabled: true
          snapshot-crds:
            enabled: true
        apps:
          cert-manager:
            enabled: true
          platform-certs:
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
        ;;
    *)
        echo "Error: PLATFORM_INSTALL_ALL must be 0 or 1." >&2
        exit 1
        ;;
esac

step 1 "Installing cilium CRDs (platform-cluster) ..."
helm upgrade --install platform-cilium-crds "${REPO_DIR}/charts/cilium-crds" \
    --namespace platform-cluster --create-namespace --wait

step 2 "Installing cilium (platform-cilium) -- waiting for Nodes to go Ready ..."
helm upgrade --install platform-cilium "${REPO_DIR}/charts/cilium" \
    --namespace platform-cilium --create-namespace --wait \
    "${cilium_args[@]}"

step 3 "Installing Flux CRDs (platform-cluster) ..."
helm upgrade --install platform-fluxcd-crds "${REPO_DIR}/charts/fluxcd-crds" \
    --namespace platform-cluster --create-namespace --wait

step 4 "Installing Flux source-controller + helm-controller (platform-fluxcd) -- waiting for controllers to be Ready ..."
helm upgrade --install platform-fluxcd "${REPO_DIR}/charts/fluxcd" \
    --namespace platform-fluxcd --create-namespace --wait \
    "${fluxcd_args[@]}"

if [[ -n "${KIND_LOCAL_GIT:-}" ]]; then
    echo
    echo "==> Installing Kind local Git server (into platform-fluxcd namespace) ..."
    deploy_kind_git_server
    kubectl rollout status deployment/components-git --namespace platform-fluxcd --timeout=60s
fi

step 5 "Installing platform components (platform-components) via Flux CD..."
platform_manifest=$(mktemp)
trap 'rm -f "${platform_manifest}"' EXIT
cat >"${platform_manifest}" <<YAML
apiVersion: v1
kind: Namespace
metadata:
  name: platform-components
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: podplane-components
  namespace: platform-components
spec:
  interval: 10m
  url: "${PLATFORM_GIT_REPOSITORY_URL:-https://github.com/podplane/components.git}"
  ref:
    branch: "${PLATFORM_GIT_REPOSITORY_BRANCH:-main}"
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
if [[ -n "${platform_values}" ]]; then
    cat >>"${platform_manifest}" <<YAML
  values:
    platform:
      components:
${platform_values}
YAML
fi
kubectl apply -f "${platform_manifest}"
kubectl wait --for=condition=Ready gitrepository/podplane-components --namespace platform-components --timeout=120s
kubectl wait --for=condition=Ready helmrelease/platform-components --namespace platform-components --timeout=300s

echo
echo "Bootstrap complete. Flux is now reconciling components."
echo "Watch reconciliation with:"
echo "  kubectl get helmreleases -A"
