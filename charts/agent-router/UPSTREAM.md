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

Review patch conflicts before updating. Agent Router extends the shared Envoy
Gateway over upstream's plaintext gRPC endpoint; it does not replace xDS TLS.

The Podplane-owned `templates/` add the cluster-internal
`platform-agent-router-gateway` Gateway, whose proxy Deployment is separate
from the ingress DaemonSet; attach AIGatewayRoutes and MCPRoutes to it. They
also add a MutatingAdmissionPolicy that shares the injected extproc socket
volume across the Pod under gVisor.
