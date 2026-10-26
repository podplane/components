# Vendored Agent Router chart

`upstream/ai-gateway-helm` is generated from the published Agent Router chart.
`SOURCE.yaml` records the release and source commit. Upstream retains the
Envoy AI Gateway chart, image and API names.

The Podplane patch replaces webhook Secret/cert-manager certificate generation
with workload certificate and ClusterTrustBundle projections, and annotates
the webhook for Podplane Operator CA injection. It also supports chart-configured
extra containers for controller TLS termination, keyed by container name so
image-only overrides preserve arguments and mounts. Image values are ordinary
strings, not template expressions. All other chart behavior,
including generation and upgrade reuse of the MCP-session Secret, stays upstream.
That session seed is cluster-local and must not be captured in seeds.

Do not edit the generated source. Refresh it with:

```shell
bash charts/agent-router/scripts/update-upstream.sh v1.2.0
```

Review patch conflicts before updating. Agent Router extends Envoy Gateway over
gRPC; the TLS configuration below does not replace Envoy Gateway's xDS TLS.

The Podplane-owned `templates/` add the GatewayClass and the cluster-internal
`platform-agent-router-gateway` Gateway, whose proxy Deployment is separate
from the ingress DaemonSet; attach AIGatewayRoutes and MCPRoutes to it. They
also add a MutatingAdmissionPolicy that shares the injected extproc socket
volume across the Pod under gVisor.

## Serving TLS

The gateway serves HTTPS on port 443; routes target the `https` listener.
Clients must trust the workload ClusterTrustBundle,
`certificates.podplane.dev:workload:roots`.

The SDS sidecar reloads projected serving certificates. Its Kubernetes Secret
contains connection details only, never certificates or keys. Health and metrics
remain unchanged.

Operator `v0.16.0` supplies workload SDS and public trust-bundle sync.

## Controller transport and plaintext isolation

Envoy Gateway uses native TLS to reach Agent Router; only the Agent Router Pod
needs an Envoy TLS-termination sidecar:

- Envoy Gateway connects to the Agent Router Service on port 1063, verifying its
  DNS name against the public CA bundle in `workload-ca` (`tls.crt`).
  `platform-components` configures the operator to create and sync that Secret
  before Agent Router starts. It contains no private key or serving certificate.
  Standalone installs must configure the same sync target; hostname overrides
  belong in `gateway-helm.config.envoyGateway.extensionManager.service.fqdn`.
- Agent Router's Service sends ports 1063 and 18002 to TLS proxy ports 1163 and
  18003 respectively. That proxy terminates TLS using the controller's existing
  workload certificate projection and forwards to the unchanged application
  listeners on loopback. Consumers of the rate-limit configuration stream must
  therefore use TLS with workload CA trust and the controller Service DNS name.
- Filesystem SDS watches the whole certificate projection directory,
  so kubelet's atomic projection replacements trigger reloads without restarting
  the proxy. Do not change these mounts to `subPath` mounts. Envoy Gateway reads
  the synced CA Secret on new TLS handshakes.
- An ingress NetworkPolicy blocks cross-Pod access to the controller's original
  plaintext ports while preserving HTTPS webhooks, metrics and kubelet probes.
  A second policy permits only HTTPS and operational ports on this gateway fleet.
  NetworkPolicies are additive: another policy granting wider ingress can reopen
  those ports. Same-Pod and local-node traffic are outside this boundary.
- A scoped, reinvoked MutatingAdmissionPolicy binds the injected MCP HTTP listener
  to `127.0.0.1:9856`, where Envoy already connects. It supports regular and native
  init sidecars. Existing proxy Pods must be replaced to receive the mutation.

These are Podplane chart changes using existing upstream configuration and
admission interfaces, not application source patches. Cross-Pod application
traffic uses TLS; plaintext remains inside Pods and on health/metrics endpoints.
Live-cluster acceptance must check CNI isolation, admission reinvocation, the
extension RPC path and serving-certificate/public-CA rotation on the pinned
Envoy image.

## Dedicated Envoy Gateway controller

Envoy Gateway calls its extension hooks for every Gateway it translates, and
fails closed, so an extension on the ingress controller would block ingress xDS
whenever Agent Router is unavailable. This chart therefore runs its own Envoy
Gateway controller as the `gateway-helm` dependency, built from
`../envoy-gateway/upstream/gateway-helm` so it carries the same Podplane patches.
It serves only the `platform-agent-router` GatewayClass, owns the extension
configuration, and disables the topology injector, which in GatewayNamespace mode
would intercept every Pod binding in the cluster. The ingress controller has no
extension and does not depend on Agent Router.

`charts/envoy-gateway/scripts/update-upstream.sh` also bumps this dependency;
keep `gateway-helm.global.images.envoyGateway.image` in `values.yaml` in sync.
