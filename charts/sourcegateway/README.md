# Source Gateway Helm Chart

This chart installs [Source Gateway](https://sourcegateway.dev/) with Podplane
workload certificates and a provider-backed encryption key.

## Encryption key

The `sourcegateway` SecretProviderBinding mounts `encryption-key` from the cluster's
default secrets provider.

The key must contain 32 random bytes encoded as base64. Preserve it: replacing it
makes Source Gateway's stored credentials unreadable.

Podplane `agentic` seed bootstrap process creates or reuses the key for AWS, Google,
and local fakevault. For manual installation, create it before enabling the component:

```console
openssl rand -base64 32 | podplane secret create --for sourcegateway -n platform-sourcegateway encryption-key --stdin
```

For managed Vault/OpenBao, provision it before agentic cluster creation in KV-v2
field `value` at
`<mount_path>/data/<key_prefix>/platform-sourcegateway/sourcegateway/encryption-key`.
Grant the `sourcegateway` workload role read access to that keyspace.
