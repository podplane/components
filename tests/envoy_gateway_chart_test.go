// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"strings"
	"testing"
)

// TestEnvoyGatewaySDSContract verifies external certificate pointers and data-plane wiring.
func TestEnvoyGatewaySDSContract(t *testing.T) {
	rendered := render(t, "../charts/envoy-gateway", "--namespace", "platform-envoy-gateway",
		"--set", "platform.envoyGateway.ingress.enabled=true",
		"--set", "platform.envoyGateway.ingress.certificates.provider=aws",
		"--set", "platform.envoyGateway.ingress.certificates.objectType=secretsmanager",
		"--set", "platform.envoyGateway.ingress.certificates.keyPrefix=clusters",
		"--set", "platform.envoyGateway.ingress.domains[0].apex=example.com")
	for _, required := range []string{
		"apiVersion: gateway.envoyproxy.io/v1alpha1", "kind: EnvoyProxy", "envoyDaemonSet:", "type: ClusterIP", "name: http-80", "containerPort: 10080", "hostPort: 80",
		"name: https-443", "containerPort: 10443", "hostPort: 443", "name: podplane-sds", `image: "ghcr.io/podplane/operator:v0.7.0"`, "name: podplane-sds\n                      emptyDir:\n                        medium: Memory",
		"kind: ServiceAccount", "name: platform-envoy-gateway-ingress-certificates", "serviceAccountName: platform-envoy-gateway-ingress-certificates",
		"driver: secrets-store.csi.k8s.io", "--socket=/var/run/podplane-sds/sds.sock",
		"--certificates-dir=/var/run/podplane/ingress-certificates", "--certificate=bundle-a379a6f6eeafb9a55e378c11=example.com",
		"type: gateway.envoyproxy.io/sds", "url: unix:///var/run/podplane-sds/sds.sock",
		"gatewayClassName: platform-envoy-gateway", `hostname: "example.com"`, `hostname: "*.example.com"`, "mode: Terminate",
		"name: platform-http-to-https-redirect-httproute", "type: RequestRedirect", "scheme: https",
		`objectName: "/clusters/platform-cluster/ingress-certificates/bundle-a379a6f6eeafb9a55e378c11"`,
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("Envoy Gateway render is missing %q", required)
		}
	}
	for _, forbidden := range []string{"tls.crt:", "tls.key:", "kubernetes.io/tls", "cert-manager.io/", "traefik.io/", "platform-acme"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("Envoy Gateway render unexpectedly contains %q", forbidden)
		}
	}
}

// TestEnvoyGatewayOpenBaoCertificateDelivery verifies local OpenBao certificate mounts.
func TestEnvoyGatewayOpenBaoCertificateDelivery(t *testing.T) {
	rendered := render(t, "../charts/envoy-gateway", "--namespace", "platform-envoy-gateway",
		"--set", "platform.envoyGateway.ingress.enabled=true",
		"--set", "platform.envoyGateway.ingress.certificates.provider=openbao",
		"--set", "platform.envoyGateway.ingress.certificates.address=https://10.0.2.15:19443/vault/local",
		"--set", "platform.envoyGateway.ingress.certificates.mountPath=secret",
		"--set", "platform.envoyGateway.ingress.certificates.authMountPath=podplane",
		"--set", "platform.envoyGateway.ingress.certificates.caCertPath=/var/run/podplane/secrets-providers/local-fakevault/ca.crt",
		"--set", "platform.envoyGateway.ingress.certificates.keyPrefix=local",
		"--set", "platform.envoyGateway.ingress.domains[0].apex=local.localhost")
	for _, required := range []string{
		"provider: openbao",
		`baoAddress: "https://10.0.2.15:19443/vault/local"`,
		`baoAuthMountPath: "podplane"`,
		`baoCACertPath: "/var/run/podplane/secrets-providers/local-fakevault/ca.crt"`,
		"roleName: platform-envoy-gateway-ingress-certificates",
		`objectName: "bundle-1f63c9f85391a14121a67337"`,
		`secretPath: "secret/data/local/platform-cluster/ingress-certificates/bundle-1f63c9f85391a14121a67337"`,
		"secretKey: value",
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("Envoy Gateway OpenBao certificate render is missing %q", required)
		}
	}
}

// TestEnvoyGatewayVaultCertificateDelivery verifies Vault certificate mounts.
func TestEnvoyGatewayVaultCertificateDelivery(t *testing.T) {
	rendered := render(t, "../charts/envoy-gateway", "--namespace", "platform-envoy-gateway",
		"--set", "platform.envoyGateway.ingress.enabled=true",
		"--set", "platform.envoyGateway.ingress.certificates.provider=vault",
		"--set", "platform.envoyGateway.ingress.certificates.address=https://vault.example",
		"--set", "platform.envoyGateway.ingress.certificates.mountPath=platform",
		"--set", "platform.envoyGateway.ingress.certificates.authMountPath=podplane",
		"--set", "platform.envoyGateway.ingress.certificates.caCertPath=/var/run/podplane/secrets-providers/vault/ca.crt",
		"--set", "platform.envoyGateway.ingress.certificates.keyPrefix=prod",
		"--set", "platform.envoyGateway.ingress.domains[0].apex=example.com")
	for _, required := range []string{
		"provider: vault",
		`vaultAddress: "https://vault.example"`,
		`vaultAuthMountPath: "podplane"`,
		`vaultCACertPath: "/var/run/podplane/secrets-providers/vault/ca.crt"`,
		"roleName: platform-envoy-gateway-ingress-certificates",
		`secretPath: "platform/data/prod/platform-cluster/ingress-certificates/bundle-a379a6f6eeafb9a55e378c11"`,
		"secretKey: value",
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("Envoy Gateway Vault certificate render is missing %q", required)
		}
	}
}

// TestEnvoyGatewayGoogleCertificateDelivery verifies the upstream GCP provider mapping.
func TestEnvoyGatewayGoogleCertificateDelivery(t *testing.T) {
	rendered := render(t, "../charts/envoy-gateway", "--namespace", "platform-envoy-gateway",
		"--set", "platform.envoyGateway.ingress.enabled=true",
		"--set", "platform.envoyGateway.ingress.certificates.provider=gcp",
		"--set", "platform.envoyGateway.ingress.certificates.projectID=project-1",
		"--set", "platform.envoyGateway.ingress.certificates.keyPrefix=clusters",
		"--set", "platform.envoyGateway.ingress.domains[0].apex=example.com")
	for _, required := range []string{
		"provider: gcp",
		`resourceName: "projects/project-1/secrets/clusters_platform-cluster_ingress-certificates_bundle-a379a6f6eeafb9a55e378c11/versions/latest"`,
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("Envoy Gateway Google certificate render is missing %q", required)
		}
	}
}

// TestEnvoyGatewayHTTPRedirectCanBeDisabled verifies users can own the HTTP catch-all route.
func TestEnvoyGatewayHTTPRedirectCanBeDisabled(t *testing.T) {
	rendered := render(t, "../charts/envoy-gateway", "--namespace", "platform-envoy-gateway",
		"--set", "platform.envoyGateway.ingress.enabled=true",
		"--set", "platform.envoyGateway.ingress.httpToHTTPSRedirect.enabled=false",
		"--set", "platform.envoyGateway.ingress.certificates.provider=aws",
		"--set", "platform.envoyGateway.ingress.certificates.objectType=secretsmanager",
		"--set", "platform.envoyGateway.ingress.domains[0].apex=example.com")
	if strings.Contains(rendered, "platform-http-to-https-redirect-httproute") {
		t.Fatal("HTTP-to-HTTPS redirect route rendered while disabled")
	}
}

// TestEnvoyGatewayComponentOrdering verifies the selected CSI provider is ordered dynamically.
func TestEnvoyGatewayComponentOrdering(t *testing.T) {
	for _, provider := range []string{"vault", "openbao"} {
		t.Run(provider, func(t *testing.T) {
			rendered := render(t, "../charts/platform-components", "--namespace", "platform-components",
				"--set", "platform.components.apps.envoy-gateway.enabled=true",
				"--set", "platform.components.values.envoy-gateway.platform.envoyGateway.ingress.enabled=true",
				"--set", "platform.components.values.envoy-gateway.platform.envoyGateway.ingress.certificates.provider="+provider)
			for _, required := range []string{"chart: ./charts/envoy-gateway", "name: envoy-gateway-crds", "name: gateway-api-crds", "name: secrets-store-csi-driver", "name: podplane-operator", "name: secrets-store-csi-provider-" + provider} {
				if !strings.Contains(rendered, required) {
					t.Errorf("component ordering is missing %q", required)
				}
			}
		})
	}
}
