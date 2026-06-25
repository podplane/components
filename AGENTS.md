# Podplane Components - Agent Guide

This repository contains Helm charts for the Podplane "Containers Layer".
The charts are reconciled in-cluster by Flux CD; the only custom code is a
single bootstrap script that deploys enough of the stack for Flux to take
over.

## Commands

- **Bootstrap a cluster**: `./bootstrap/apply.sh` — installs
  cilium, fluxcd and the platform-components chart in dependency order against the
  current `kubectl` context.
- **Bootstrap a local Podplane VM from local sources**: create a bare local VM
  from the Podplane CLI repo with `podplane local start --components=none`,
  then run `make git-sync` and one of `make minimal`, `make recommended`, or
  `make all` from this repo. Use `make git-watch` to re-sync automatically on
  file changes.
- **Lint all charts**: `make lint`.
- **Template all charts (no install)**: `make render`.
- **Validate vendored CRDs**: `make check-crds`.
- **Update vendored CRDs**: `make update-crds` (or `make update-crds CHART=<name>`).

Dependencies: `helm`, `kubectl`; `watchexec` is optional for `make git-watch`.

Note: agents must NEVER run `./bootstrap/apply.sh`, `make minimal`, `make recommended`,
or `make all` against a real cluster without explicit user approval.

## Architecture

- **Bootstrap**: [`bootstrap/apply.sh`](./bootstrap/apply.sh) installs five
  Helm releases in order with `helm upgrade --install --create-namespace
  --wait` — `platform-cilium-crds`, `platform-cilium`, `platform-coredns`,
  `platform-fluxcd-crds`, `platform-fluxcd` — then renders the bootstrap chart
  with `helm template` and `kubectl apply`s a `platform-components` `Namespace`,
  a `GitRepository` pointing at this repo, and a Flux `HelmRelease` that
  installs the `platform-components` chart. From there Flux reconciles
  everything else.
- **Platform components chart** ([`charts/platform-components`](./charts/platform-components)): the single source
  of truth at runtime. Its `values.yaml` lists every Core and Addon
  component. The chart renders:
    - One Flux `HelmRelease` per enabled CRD chart and per enabled app chart.
    - Cluster-scoped namespaces (`platform-cluster`, `default`, plus a
      `platform-<name>` namespace per enabled app where
      `manageNamespace` is not `false`).
- **Platform policy charts**: `charts/platform-rbac` contains Podplane RBAC and
  admission policy resources; `charts/platform-trust` contains Podplane trust
  bundle and trust policy resources.
- **Flux CD** ([`charts/fluxcd`](./charts/fluxcd) + [`charts/fluxcd-crds`](./charts/fluxcd-crds)):
  Helm-only. Only `source-controller` and `helm-controller` are deployed;
  `kustomize-controller`, `notification-controller`,
  `image-automation-controller` and `image-reflection-controller` are all
  disabled to keep pod count minimal. The CRDs chart ships CRDs for *all*
  Flux controllers however, so users running their own additional Flux
  controllers alongside the system installation don't have to install
  missing CRDs themselves.
- **Component charts** (`charts/<name>`): each is a thin
  Helm chart that depends on an upstream chart (cilium, cert-manager,
  traefik, etc.) or vendors upstream CRDs in `templates/external/`. Vendored
  CRDs are updated through the shared `scripts/crds` Go command.
- **Post-bootstrap changes**: edit the platform chart's values and let Flux
  reconcile. The `podplane install` / `podplane uninstall` CLI commands
  toggle entries in `platform.components.{apps,crds}`.

## Conventions

- **Namespaces**: every component lives in `platform-<name>`. Cluster-scoped
  releases (CRD charts, `platform-rbac`) use the shared `platform-cluster` namespace.
  Flux runs in `platform-fluxcd` (not the upstream `flux-system` default) so
  users may run their own Flux installation in `flux-system` alongside ours
  if they choose.
- **Helm release names**: `platform-<name>` (e.g. `platform-cilium`,
  `platform-cert-manager`). Bootstrap uses the same names so Flux can adopt
  the existing releases when it takes over.
- **Network**: dual-stack IPv4/IPv6 with Podplane's standard CIDRs:
  Pod IPv4 `100.64.0.0/10`, IPv6 `fd64::/48`,
  Service IPv4 `198.18.0.0/15`, IPv6 `fdc6::/108`.
  CoreDNS lives at `198.19.255.254` / `fdc6::ffff`.
- **CRDs**: ship as a separate `<name>-crds` chart per
  [Helm best practice](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/).
  App HelmReleases set `install.crds: Skip` and `upgrade.crds: Skip`; the
  matching `<name>-crds` HelmRelease handles them.
- **Bash scripts**: strict mode (`set -eo pipefail`).
- **Naming**: kebab-case for chart names; `platform-` prefix for cluster
  components.
- **Dependencies on upstream charts**: declared in each chart's
  `Chart.yaml` `dependencies:` block, not vendored as subcharts.

## Scope

- The only officially supported Kubernetes distribution/platform/provider is Podplane.
- Helm only. No plain manifests, no kustomize, and no other tools.
