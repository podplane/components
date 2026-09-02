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
    "providers": {
      "openbao-local": {
        "kind": "openbao",
        "key_prefix": "shared-secrets",
        "address": "https://bao.example",
        "mount_path": "secret"
      }
    }
  },
  "registry": {
    "auth": {
      "enabled": true
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
```

Secret material must not be placed in the operator config. Use Kubernetes
Secrets, workload identity, IAM, or the provider's native credential mechanism.

The chart passes scoped serving flags for each HTTPS endpoint:
`--aggregated-api-*` for the aggregated Kubernetes API backend and
`--registry-auth-*` for Docker registry token exchange. When
`registry.auth.enabled` is true, the chart exposes a separate HTTPS registry auth
Service. The auth service reuses the same service-DNS certificate as the
aggregated API. The chart adds the `svc.cluster.local` SAN for the
`registry-auth` Service and renders a Gateway `BackendTLSPolicy` that trusts
`platform-selfsigned-ca-bundle`, matching the Podplane web template workload
ingress pattern.

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

## SecretProviderClass restriction bypass

The chart renders a default ValidatingAdmissionPolicy that prevents pods from
mounting a Secrets Store CSI `SecretProviderClass` unless its name matches the
pod `serviceAccountName`. Pods with Secrets Store CSI volumes must set
`serviceAccountName` explicitly; omitted or empty service account names are
denied rather than treated as `default`. Cluster admins can optionally enable a
bypass scoped per-namespace by defining the following label on each namespace:

```yaml
metadata:
  labels:
    secrets.podplane.dev/secretproviderclass-restriction-policy: bypass
```

Enable that bypass with:

```yaml
podplane:
  operator:
    admissionPolicies:
      secretProviderClassRestriction:
        excludeNamespacesWithLabel:
          enabled: true
```

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
| `podplane.operator.registry.auth.securePort` | HTTPS container port for Docker registry auth; the Service exposes port 443. |
| `podplane.operator.registry.auth.backendTLSPolicy.enabled` | Renders Gateway backend TLS trust for the registry auth Service. |
| `podplane.operator.registry.auth.backendTLSPolicy.caCertificateRef.name` | ConfigMap containing the CA bundle the Gateway trusts for registry auth backend TLS. |
| `podplane.operator.config.cluster.id` | Podplane cluster ID; required for real installs. |
| `podplane.operator.config.cluster.oidc.issuerURL` | Podplane OIDC issuer; required when registry auth is enabled. |
| `podplane.operator.config.cluster.oidc.clientID` | Podplane OIDC audience/client ID; defaults to `cluster.id` in operator config. |
| `podplane.operator.config.secrets.keyRotation` | Operator public-key rotation interval. |
| `podplane.operator.config.secrets.allowSyncToKubernetesSecrets` | Allows `syncToKubernetesSecrets` when namespaces opt in. |
| `podplane.operator.config.secrets.providers` | Non-sensitive provider config map. Each provider may set `keyPrefix`; it defaults to `cluster.id` when omitted. |
| `podplane.operator.config.registry.auth.enabled` | Enables the HTTPS Docker registry auth endpoint. |
| `podplane.operator.tls.secretName` | TLS Secret mounted by the operator Deployment. |
| `podplane.operator.tls.issuerRef` | cert-manager issuer reference for the serving certificate. |
| `podplane.operator.apiService.enabled` | Renders the Kubernetes APIService registration. |
| `podplane.operator.apiService.group` | APIService group served by the operator. |
| `podplane.operator.apiService.version` | APIService version served by the operator. |
| `podplane.operator.rbac.defaultRoles.enabled` | Renders default Podplane Secrets ClusterRoles. |
| `podplane.operator.rbac.defaultRoles.namePrefix` | Name prefix for default Podplane Secrets ClusterRoles. |
| `podplane.operator.admissionPolicies.secretProviderClassRestriction.enabled` | Renders the default ValidatingAdmissionPolicy restricting direct SecretProviderClass mounts. |
| `podplane.operator.admissionPolicies.secretProviderClassRestriction.excludeNamespacesWithLabel.enabled` | Excludes namespaces with the configured label from the SecretProviderClass restriction. |
| `podplane.operator.admissionPolicies.secretProviderClassRestriction.excludeNamespacesWithLabel.key` | Namespace label key used for SecretProviderClass restriction bypass. |
| `podplane.operator.admissionPolicies.secretProviderClassRestriction.excludeNamespacesWithLabel.value` | Namespace label value used for SecretProviderClass restriction bypass. |
| `podplane.operator.extraVolumes` | Additional pod volumes. |
| `podplane.operator.extraVolumeMounts` | Additional operator container mounts. |
| `podplane.operator.env` | Additional operator environment variables. |
| `podplane.operator.envFrom` | Additional operator `envFrom` entries. |
| `podplane.operator.resources` | Operator container resource requests/limits. |
| `podplane.operator.nodeSelector` | Pod node selector. |
| `podplane.operator.tolerations` | Pod tolerations. |
| `podplane.operator.affinity` | Pod affinity. |
