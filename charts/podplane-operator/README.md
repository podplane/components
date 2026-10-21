# Podplane Operator Helm Chart

This chart installs the [Podplane operator](https://github.com/podplane/operator) deployment and the
`secrets-api.podplane.dev` aggregated APIService. The matching CRDs are shipped
separately in the `podplane-operator-crds` chart.

## Installation model

Podplane normally installs this chart through `platform-components` by enabling the app & CRD and setting values like so:

```yaml
platform:
  components:
    crds:
      podplane-operator-crds:
        enabled: true
    apps:
      podplane-operator:
        enabled: true
    values:
      podplane-operator:
        podplane:
          operator:
            config:
              cluster:
                id: dev-cluster
                oidc:
                  issuerURL: https://oidc.example/dev-cluster
                  clientID: dev-cluster
              secrets:
                defaultProvider: openbao-local
                allowSyncToKubernetesSecrets: false
                providers:
                  openbao-local:
                    kind: openbao
                    keyPrefix: shared-secrets
                    address: https://bao.example
                    mountPath: secret
              registry:
                auth:
                  enabled: true
              ingressCertificates:
                provider: openbao-local
                acme:
                  server: https://acme-v02.api.letsencrypt.org/directory
                  email: ops@example.com
                domains:
                  staging.example.com:
                    dnsProvider:
                      kind: aws-route53
                      region: us-east-1
                      hostedZoneID: Z123456789
                      roleARN: arn:aws:iam::123456789012:role/podplane-acme
```

`podplane.operator.config.cluster.id` is required.

## Runtime configuration

The chart renders a JSON config file and passes it to the operator with the real
`podplane-operator --config` flag:

```json
{
  "cluster": {
    "id": "dev-cluster",
    "oidc": {
      "issuer_url": "https://oidc.example/dev-cluster",
      "client_id": "dev-cluster",
      "username_claim": "email",
      "groups_claim": "groups"
    }
  },
  "secrets": {
    "key_rotation": "6h",
    "allow_sync_to_kubernetes_secrets": false,
    "default_provider": "openbao-local",
    "providers": {
      "openbao-local": {
        "kind": "openbao",
        "key_prefix": "shared-secrets",
        "address": "https://bao.example",
        "mount_path": "secret"
      }
    }
  },
  "ingress_certificates": {
    "provider": "openbao-local",
    "acme": {
      "server": "https://acme-v02.api.letsencrypt.org/directory",
      "email": "ops@example.com"
    },
    "domains": {
      "staging.example.com": {
        "dns_provider": {
          "kind": "aws-route53",
          "region": "us-east-1",
          "hosted_zone_id": "Z123456789",
          "role_arn": "arn:aws:iam::123456789012:role/podplane-acme"
        }
      }
    }
  }
}
```

When configured through `platform-components`, the values of the JSON config file are determined by this chart's values
under `platform.components.values.podplane-operator`:

```yaml
platform:
  components:
    values:
      podplane-operator:
        podplane:
          operator:
            config:
              cluster:
                id: dev-cluster
                oidc:
                  issuerURL: https://oidc.example/dev-cluster
                  clientID: dev-cluster
              secrets:
                defaultProvider: openbao-local
                allowSyncToKubernetesSecrets: false
                providers:
                  openbao-local:
                    kind: openbao
                    keyPrefix: shared-secrets
                    address: https://bao.example
                    mountPath: secret
              ingressCertificates:
                provider: openbao-local
                domains:
                  staging.example.com: {}
```

Secret material must not be placed in the operator config. Use Kubernetes
Secrets, workload identity, IAM, or the provider's native credential mechanism.

The chart configures the aggregated Kubernetes API backend with
`--aggregated-api-*` serving flags and a workload-CA-signed service certificate.
Registry authentication is handled directly by Zot, not the operator.

## Metrics

The operator exposes certificate expiry, signer health, and pending certificate
requests for your monitoring system to collect. Set
`podplane.operator.metrics.enabled: true` to serve `/metrics` inside the cluster
on port 8080. Metrics are disabled by default; the chart does not install a
collector or alert rules.

To scrape pods directly instead of through the metrics Service, set
`podplane.operator.metrics.service.enabled: false`. The listener and
NetworkPolicy remain enabled.

Set `podplane.operator.metrics.allowedPeers` to allow your collector through the
NetworkPolicy; the default empty list blocks access. Each peer should select both
the collector's namespace and pods using labels controlled by cluster admins.
The endpoint has no authentication, so keep it private to trusted collectors.

## Mounting provider credentials

The operator reads Vault/OpenBao tokens from:

```text
/var/run/podplane/providers/<provider-name>/token
```

Mount a Kubernetes Secret at that path with `extraVolumes` and
`extraVolumeMounts`:

```yaml
podplane:
  operator:
    extraVolumes:
      - name: openbao-local-token
        secret:
          secretName: openbao-local-token
    extraVolumeMounts:
      - name: openbao-local-token
        mountPath: /var/run/podplane/providers/openbao-local
        readOnly: true
```

The chart also supports optional `env` and `envFrom` for provider SDK settings
or Secret-backed environment variables.

## SecretProviderClass policies

The `platform-rbac` chart owns the cluster-wide secrets admission policies,
so they remain enforced when the operator is uninstalled. Configure the optional
namespace bypass in [that chart's values](../platform-rbac/values.yaml) under
`platform.rbac.admissionPolicies.secretProviderClassRestriction`.

## Values

Common values:

| Value | Purpose |
| --- | --- |
| `podplane.operator.namePrefix` | Name prefix for operator-owned Kubernetes resources. |
| `podplane.operator.image.repository` | Operator image repository. |
| `podplane.operator.image.tag` | Operator image tag. |
| `podplane.operator.image.pullPolicy` | Operator image pull policy. |
| `podplane.operator.serviceAccountName` | ServiceAccount name used by the operator Deployment. |
| `podplane.operator.aggregatedApiService.securePort` | HTTPS container port for aggregated API backends. |
| `podplane.operator.config.cluster.id` | Podplane cluster ID; required for real installs. |
| `podplane.operator.config.cluster.oidc.issuerURL` | Podplane OIDC issuer. |
| `podplane.operator.config.cluster.oidc.clientID` | Podplane OIDC audience/client ID; defaults to `cluster.id` in operator config. |
| `podplane.operator.config.secrets.keyRotation` | Operator public-key rotation interval. |
| `podplane.operator.config.secrets.allowSyncToKubernetesSecrets` | Allows `syncToKubernetesSecrets` when namespaces opt in. |
| `podplane.operator.config.secrets.defaultProvider` | Default external backend; also used for operator-owned ingress certificate state unless overridden. |
| `podplane.operator.config.secrets.providers` | Non-sensitive provider config map. Each provider may set `keyPrefix`; it defaults to `cluster.id` when omitted. |
| `podplane.operator.config.ingressCertificates` | Apex-keyed ingress certificate configuration. The operator always publishes a self-signed fallback and optionally replaces supported domains through ACME. |
| `podplane.operator.apiService.enabled` | Renders the Kubernetes APIService registration. |
| `podplane.operator.apiService.group` | APIService group served by the operator. |
| `podplane.operator.apiService.version` | APIService version served by the operator. |
| `podplane.operator.rbac.defaultRoles.enabled` | Renders default Podplane Secrets ClusterRoles. |
| `podplane.operator.rbac.defaultRoles.namePrefix` | Name prefix for default Podplane Secrets ClusterRoles. |
| `podplane.operator.extraVolumes` | Additional pod volumes. |
| `podplane.operator.extraVolumeMounts` | Additional operator container mounts. |
| `podplane.operator.env` | Additional operator environment variables. |
| `podplane.operator.envFrom` | Additional operator `envFrom` entries. |
| `podplane.operator.resources` | Operator container resource requests/limits. |
| `podplane.operator.nodeSelector` | Pod node selector. |
| `podplane.operator.tolerations` | Pod tolerations. |
| `podplane.operator.affinity` | Pod affinity. |
