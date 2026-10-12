# Nstance Operator Helm Chart

This chart installs the [Nstance operator](https://nstance.dev/docs/components/nstance-operator/),
which synchronizes Cluster API resources with Nstance servers, manages instance
groups and on-demand nodes, and coordinates node draining. Each deployment
manages one Nstance cluster and tenant; it does not deploy Nstance servers.

## Installation

Podplane installs this chart through `platform-components` in
`platform-nstance-operator`. Enable the `nstance-operator` app and
`nstance-operator-crds` CRD chart together with Cluster API and its dependencies;
see the [platform values](../platform-components/values.yaml).

The defaults run two leader-elected replicas on control-plane nodes. Webhook
certificates use Podplane's workload certificate signer, not cert-manager.

## Configuration

Pass chart values under `platform.components.values.nstance-operator`.
Cluster identity and shard endpoints are required:

```yaml
config:
  clusterID: example-cluster
  tenant: default
  shards:
    us-east-1a:
      registration_addr: "172.16.0.100:8992"
      operator_addr: "172.16.0.100:8993"
```

Before initial registration, provision these resources in the operator namespace:

- `nstance-operator-nonce`: a dedicated Secret with key `nonce.jwt`.
- `nstance-cluster-ca`: a ConfigMap with key `ca.crt`.

Names are configurable through `registrationSecret.name` and `clusterCA.name`.
Keep nonce bytes out of Helm values. The operator stores its credentials and
deletes the nonce Secret after durable registration. See the official
[registration guide](https://nstance.dev/docs/components/nstance-operator/#registration)
and [chart values](./values.yaml) for further configuration, including the option
to create the public CA ConfigMap. For external workload clusters, see the
[Cluster API guide](https://nstance.dev/docs/reference/cluster-api/).

## Sleep

Automatic sleep is enabled by default, with a 30-minute inactivity window.
Set `config.sleep.enabled: false` to disable it. See
[Sleep and Wake](https://nstance.dev/docs/features/sleep-and-wake/) for activity
guards, routing, and wake behavior.

CAPI Cluster RBAC determines who may request sleep. The chart's admission
policy validates requests and protects operator-owned progress and cleanup.
