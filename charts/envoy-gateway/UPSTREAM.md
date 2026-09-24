# Vendored Envoy Gateway chart

`upstream/gateway-helm` is generated from Envoy Gateway's `charts/gateway-helm`
chart. `SOURCE.yaml` records the upstream repository, version, and commit. The
original source is available under the Apache License 2.0.

## Why

The published chart unconditionally runs a pre-install and pre-upgrade
`certgen` Job that generates Envoy Gateway TLS credentials and stores their
private keys in Kubernetes Secrets. Podplane vendors the chart so that this
bootstrap path and use of Kubernetes Secrets can be replaced with workload
certificate projections. The Podplane changes are intentionally limited to:

- removing the certgen Job and its RBAC;
- projecting the Envoy Gateway serving key and certificate from the Podplane
  workload signer;
- projecting that signer's ClusterTrustBundle as `/certs/ca.crt`; and
- annotating the topology injector webhook so Podplane Operator injects the
  workload CA bundle.

The goal is to find a way to have these limitations resolved upstream,
and remove this copy and the custom changes.

## Limitations

The Podplane chart supports the shared Gateway and HTTPRoute data plane used by
the platform. Envoy Gateway features that depend on certgen's additional
`envoy`, `envoy-rate-limit`, or `envoy-oidc-hmac` Secrets — such as global rate
limiting, Wasm extensions, and OIDC/OAuth2 SecurityPolicies — are not supported
by this chart until their credentials can also be supplied without Kubernetes
Secrets.

## Updating

`patches/gateway-helm.patch` is the authoritative Podplane delta. Do not edit
the generated source directly. To import a new upstream release and run the
focused validation:

```shell
./charts/envoy-gateway/scripts/update-upstream.sh v1.9.0
```

Patch conflicts intentionally stop the update so upstream changes can be
reviewed before refreshing the patch.
