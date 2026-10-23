# Vendored Agent Router chart

`upstream/ai-gateway-helm` is generated from the published Agent Router chart.
`SOURCE.yaml` records the release and source commit. Upstream retains the
Envoy AI Gateway chart, image and API names.

The Podplane patch replaces webhook Secret/cert-manager certificate generation
with workload certificate and ClusterTrustBundle projections, and annotates
the webhook for Podplane Operator CA injection. All other chart behavior,
including generation and upgrade reuse of the MCP-session Secret, stays upstream.
That session seed is cluster-local and must not be captured in seeds.

Do not edit the generated source. Refresh it with:

```shell
bash charts/agent-router/scripts/update-upstream.sh v1.2.0
```

Review patch conflicts before updating. Agent Router extends Envoy Gateway over
upstream's plaintext gRPC endpoint; it does not replace xDS TLS.

The Podplane-owned `templates/` add the GatewayClass and the cluster-internal
`platform-agent-router-gateway` Gateway, whose proxy Deployment is separate
from the ingress DaemonSet; attach AIGatewayRoutes and MCPRoutes to it. They
also add a MutatingAdmissionPolicy that shares the injected extproc socket
volume across the Pod under gVisor.

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
