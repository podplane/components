# Podplane Kubernetes PaaS Components

Podplane `components` is a collection of Helm charts for the components
which run atop a bare Podplane Kubernetes cluster to deliver a
Platform-as-a-Service (PaaS) experience. 

The charts are reconciled in-cluster by
[Flux CD](https://fluxcd.io/), driven by the [`charts/platform-components`](./charts/platform-components)
chart's values.

## Components

__Core (always installed):__

- [Cilium](https://cilium.io/) for cluster networking & policies (_Apache 2.0_)
- [CoreDNS](https://coredns.io/) for DNS resolution (_Apache 2.0_)
- [Flux CD](https://fluxcd.io/) `source-controller` + `helm-controller` for
  in-cluster Helm reconciliation (_Apache 2.0_). `kustomize-controller`,
  `notification-controller` and the image controllers are intentionally
  disabled to keep pod count minimal. CRDs for all Flux controllers are
  shipped however, so users may run additional Flux controllers alongside
  the system installation.
- [Gateway API CRDs](https://gateway-api.sigs.k8s.io/) for ingress controllers
  using Gateway API.
- `platform-components`: the Podplane platform chart that manages reserved
  namespaces and the Flux `HelmRelease` resources for every other component.
- `platform-rbac`: default RBAC and admission policy configuration.

__Addons (optional, installable via `podplane install <name>`):__

- [cert-manager](https://cert-manager.io/) for TLS certificate management
  (_Apache 2.0_).
- [trust-manager](https://cert-manager.io/docs/trust/trust-manager/) for
  certificate trust store management (_Apache 2.0_).
- `platform-trust` for Podplane trust bundles and trust policy resources.
- [Traefik](https://traefik.io/) for ingress (_MIT_).
- [Snapshot Controller](https://github.com/kubernetes-csi/external-snapshotter)
  for persistent volume snapshotting (_Apache 2.0_).
- [AWS EBS CSI Driver](https://github.com/kubernetes-sigs/aws-ebs-csi-driver)
  for persistent storage on AWS (_Apache 2.0_).

## Usage

After cluster creation, bootstrap Cilium, Flux CD and the platform chart
against the current `kubectl` context:

```sh
./bootstrap.sh
```

This bootstraps five component layers in dependency order:

1. `platform-cilium-crds` (in `platform-cluster`)
2. `platform-cilium` (in `platform-cilium`) — nodes go `Ready` after this
3. `platform-fluxcd-crds` (in `platform-cluster`)
4. `platform-fluxcd` (in `platform-fluxcd`)
5. `platform-components` (as a Flux `HelmRelease` in `platform-components`)

From this point on, Flux reconciles `platform-components` and every other component
from the `charts/platform-components` chart's values, fetched from this repo via a
bootstrap `GitRepository` resource. To enable / disable / reconfigure a component, edit
[`charts/platform-components/values.yaml`](./charts/platform-components/values.yaml) (or the
corresponding `HelmRelease` in-cluster) and let Flux pick it up.

```sh
kubectl get helmreleases -A
```

## Local Testing

We use [Kind](https://kind.sigs.k8s.io/) to test the configuration locally:

```sh
make kind-create     # create the local Kind cluster + switch kubectl context to it
make bootstrap       # install the components from github.com/podplane/components
make kind-delete     # tear down
```

### Testing unpushed local chart changes

The above works fine if you've pushed changes to the [github.com/podplane/components](https://github.com/podplane/components) main branch.

But what if you want to test some changes before committing and pushing them?

The makefile contains different commands, which copies of all local changes into a temporary "shadow git" repository that's served by an in-cluster git server which Flux will use instead of github.com/podplane/components - to do this, use these commands instead:

```sh
make dev-sync        # publish current local files to temp/kind-git/components.git
make kind-create     # mounts temp/kind-git into the Kind node
make dev-bootstrap   # deploys the Git daemon and points Flux at local-dev
make kind-delete     # tear down
```

After bootstrap, run `make dev-sync` again for manual updates, or
`make dev-watch` to sync automatically on file changes.

## Repository Layout

- `charts/<name>/` — component Helm charts. App charts are thin wrappers around
  upstream charts declared in `Chart.yaml` `dependencies:`.
- `charts/<name>-crds/` — sibling CRD chart per
  [Helm Best Practices for CRDs](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/#method-2-separate-charts).
  Each ships vendored upstream CRD YAML in `templates/external/`; updates are
  handled by the shared `scripts/crds` Go command.
- `charts/platform-components/` — the platform chart that emits Flux
  `HelmRelease` resources for components and manages platform namespaces.
- `charts/platform-rbac/` — Podplane platform RBAC and admission policies.
- `charts/platform-trust/` — Podplane trust bundles and trust policy resources.
- `bootstrap.sh` — the only custom shell code; brings the cluster from
  bare to Flux-managed. Creates the `GitRepository` resource used by all Flux CD `HelmRelease` resources created.

## CRD Compatibility Policy

Podplane-managed CRDs are upgraded only across upstream-supported compatibility
windows. Podplane may remove deprecated served CRD versions only in a documented
breaking components release, with migration notes.

The shared `scripts/crds` updater must preserve every previously served CRD API
version unless the change is intentionally handled as a breaking release. It
downloads or renders upstream CRDs into a temporary directory, compares them
against the currently vendored CRDs, and only replaces `templates/external/`
after the compatibility check passes. The check fails if a CRD is removed or
stops serving a previously served version.

Use `make update-crds` to update every Podplane-managed CRD chart, or
`make update-crds CHART=<name>` for one chart.

## Cluster Design Assumptions

The platform defaults assume Podplane's standard dual-stack network design:

* We assume Kubernetes is configured with dual-stack IPv4 + IPv6.

  * Pod IPv4 CIDR block is `100.64.0.0/10`, supporting up to 4,194,304 IPv4
    addresses. RFC 6598 reserves this CIDR block for Carrier-Grade NAT.

  * Pod IPv6 CIDR block is `fd64::/48`.

  * Service IPv4 CIDR block is `198.18.0.0/15`, supporting up to 131,072 IPv4
    addresses.

  * Service IPv6 CIDR block is `fdc6::/108`.

    * Note that kube-apiserver requires a prefix length >= 108.

  * Both IPv4 CIDR blocks are defined as private networks
    <https://en.wikipedia.org/wiki/Reserved_IP_addresses>

  * Both IPv4 CIDR blocks fall within the default set of eBPF-based
    nonMasqueradeCIDRs  <https://docs.cilium.io/en/stable/network/concepts/masquerading/>

  * Both IPv4 CIDR blocks are configured on `kube-controller-manager`.
    The service CIDR blocks are configured on `kube-apiserver`.
    We also configure per-Node CIDR blocks with `/24` prefix length for IPv4, and `/64` prefix length for IPv6.

* We configure Cilium CNI to use Kubernetes IPAM mode.

* CoreDNS runs as a DaemonSet

  * It uses the last service IPv4, `198.19.255.254`

  * It uses the last service IPv6, `fdc6::ffff`

  * The kubelet is configured to use the above two addresses as clusterDNS.

## Learn More

For more, see the official Podplane site at
[podplane.dev](https://podplane.dev), and the components documentation at
[podplane.dev/docs/components](https://podplane.dev/docs/components).

## License

Podplane is licensed under the Apache License, Version 2.0.
Copyright 2026 Nadrama Pty Ltd.

See the [LICENSE](./LICENSE) file for details.
