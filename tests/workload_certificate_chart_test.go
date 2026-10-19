// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// TestOperatorCertificateMetrics verifies optional metrics exposure and collector access restrictions.
func TestOperatorCertificateMetrics(t *testing.T) {
	base := []string{"--namespace", "platform-podplane-operator", "-f", "values/podplane-operator.yaml"}
	disabled := render(t, "../charts/podplane-operator", base...)
	for _, forbidden := range []string{"--metrics-bind-address", "kind: ServiceMonitor", "kind: PrometheusRule", "kind: NetworkPolicy", "targetPort: metrics"} {
		if strings.Contains(disabled, forbidden) {
			t.Errorf("disabled metrics contains %q", forbidden)
		}
	}
	args := append(append([]string{}, base...), "--set", "podplane.operator.metrics.enabled=true", "--set", "podplane.operator.metrics.port=9095")
	args = append(args, "--set", "podplane.operator.metrics.allowedPeers[0].namespaceSelector.matchLabels.monitoring=trusted",
		"--set", "podplane.operator.metrics.allowedPeers[0].podSelector.matchLabels.app=prometheus")
	enabled := render(t, "../charts/podplane-operator", args...)
	if strings.Contains(enabled, "kind: ServiceMonitor") || strings.Contains(enabled, "kind: PrometheusRule") {
		t.Fatal("metrics must not install collection or alerting resources")
	}
	for _, required := range []string{"--metrics-bind-address=:9095", "containerPort: 9095", "port: 9095", "targetPort: metrics", "publishNotReadyAddresses: true", "kind: NetworkPolicy", "namespaceSelector:", "podSelector:"} {
		if !strings.Contains(enabled, required) {
			t.Errorf("metrics render missing %q", required)
		}
	}
	var service, deployment map[string]any
	for _, doc := range chartDocuments(t, enabled) {
		switch doc["kind"] {
		case "Service":
			if chartMap(doc, "metadata")["name"] == "platform-podplane-operator-metrics" {
				service = doc
			}
		case "Deployment":
			deployment = doc
		}
	}
	if service == nil || deployment == nil {
		t.Fatal("missing metrics Service or Deployment")
	}
	if !reflect.DeepEqual(chartMap(service, "spec", "selector"), chartMap(deployment, "spec", "template", "metadata", "labels")) {
		t.Fatal("metrics Service does not select operator pods")
	}
	t.Run("direct pod scraping", func(t *testing.T) {
		direct := render(t, "../charts/podplane-operator", append(args, "--set", "podplane.operator.metrics.service.enabled=false")...)
		for _, doc := range chartDocuments(t, direct) {
			if doc["kind"] == "Service" && chartMap(doc, "metadata")["name"] == "platform-podplane-operator-metrics" {
				t.Fatal("metrics Service rendered when disabled")
			}
		}
		for _, required := range []string{"--metrics-bind-address=:9095", "containerPort: 9095", "kind: NetworkPolicy", "port: 9095", "monitoring: trusted", "app: prometheus"} {
			if !strings.Contains(direct, required) {
				t.Errorf("direct pod scraping render missing %q", required)
			}
		}
	})
	for _, invalid := range []string{"podplane.operator.metrics.enabled=true,podplane.operator.metrics.port=8443", "podplane.operator.metrics.enabled=true,podplane.operator.metrics.port=80"} {
		command := append([]string{"template", "test", "../charts/podplane-operator", "--kube-version", "1.37.0"}, base...)
		command = append(command, "--set", invalid)
		if out, err := exec.Command("helm", command...).CombinedOutput(); err == nil {
			t.Fatalf("invalid metrics configuration accepted: %s\n%s", invalid, out)
		}
	}
}

// TestWorkloadCertificateContract verifies workload CA mounting and signer configuration.
func TestWorkloadCertificateContract(t *testing.T) {
	base := []string{"--namespace", "platform-podplane-operator", "-f", "../tests/values/podplane-operator.yaml"}
	enabledArgs := append(base,
		"--set", "podplane.operator.config.secrets.providers.aws.objectType=ssmparameter",
		"--set", "podplane.operator.config.cluster.spiffe.trustDomain=cluster.example",
	)
	enabled := render(t, "../charts/podplane-operator", enabledArgs...)
	for _, required := range []string{
		"kind: SecretProviderClass", "name: platform-podplane-operator",
		`objectName: "/test-cluster/workload-ca-key"`, "objectType: \"ssmparameter\"", "objectAlias: workload-ca-key",
		"mountPath: /var/run/podplane/certificates", "readOnly: true", "type: Recreate", "resources: [\"podcertificaterequests\"]",
		"resourceNames: [\"certificates.podplane.dev/workload\"]", "verbs: [\"sign\", \"attest\"]",
		"resourceNames: [\"certificates.podplane.dev:workload:roots\"]", "resources: [\"pods\", \"services\"]",
		"reserved-workload-ca-spc-vap",
		"\"trust_domain\": \"cluster.example\"", "\"certificates\":", "\"ca_path\": \"/var/run/podplane/certificates/workload-ca-key\"",
	} {
		if !strings.Contains(enabled, required) {
			t.Errorf("enabled render is missing %q", required)
		}
	}
	for _, forbidden := range []string{"kind: Secret\n", "Podplane Workload CA", "providerCredentials", "helm.sh/hook", "subPath:", "\"workload_certificates\""} {
		if strings.Contains(enabled, forbidden) {
			t.Errorf("workload contract unexpectedly contains %q", forbidden)
		}
	}
}

// TestGoogleWorkloadCertificateContract verifies the upstream GCP provider mapping.
func TestGoogleWorkloadCertificateContract(t *testing.T) {
	rendered := render(t, "../charts/podplane-operator",
		"--namespace", "platform-podplane-operator", "-f", "../tests/values/podplane-operator.yaml",
		"--set", "podplane.operator.config.secrets.defaultProvider=google-secret-manager",
		"--set", "podplane.operator.config.secrets.providers.google-secret-manager.kind=gcp",
		"--set", "podplane.operator.config.secrets.providers.google-secret-manager.keyPrefix=shared",
		"--set", "podplane.operator.config.secrets.providers.google-secret-manager.projectID=project-1",
	)
	for _, required := range []string{
		"provider: gcp",
		`resourceName: "projects/project-1/secrets/shared_workload-ca-key/versions/latest"`,
		"path: workload-ca-key",
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("Google workload certificate render is missing %q", required)
		}
	}
}

// TestOpenBaoWorkloadCertificateContract verifies the OpenBao workload key mount.
func TestOpenBaoWorkloadCertificateContract(t *testing.T) {
	rendered := render(t, "../charts/podplane-operator", "--namespace", "platform-podplane-operator",
		"--set", "podplane.operator.config.cluster.id=local",
		"--set", "podplane.operator.config.cluster.spiffe.trustDomain=local.k8s.localhost",
		"--set", "podplane.operator.config.secrets.defaultProvider=local-fakevault",
		"--set", "podplane.operator.config.secrets.providers.local-fakevault.kind=openbao",
		"--set", "podplane.operator.config.secrets.providers.local-fakevault.address=https://10.0.2.15:19443/vault/local",
		"--set", "podplane.operator.config.secrets.providers.local-fakevault.mountPath=secret",
		"--set", "podplane.operator.config.secrets.providers.local-fakevault.authPath=auth/podplane",
		"--set", "podplane.operator.config.secrets.providers.local-fakevault.caCert=local-ca")
	for _, required := range []string{
		"provider: openbao",
		`baoAddress: "https://10.0.2.15:19443/vault/local"`,
		`baoAuthMountPath: "podplane"`,
		`baoCACertPath: "/var/run/podplane/secrets-providers/local-fakevault/ca.crt"`,
		`roleName: "podplane-operator"`,
		"objectName: workload-ca-key",
		`secretPath: "secret/data/local/workload-ca-key"`,
		"secretKey: value",
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("OpenBao workload certificate render is missing %q", required)
		}
	}
}

// TestLocalBootstrapCertificates verifies local ingress uses node certificates
// while retaining OpenBao for secrets and the workload CA.
func TestLocalBootstrapCertificates(t *testing.T) {
	rendered := render(t, "../bootstrap",
		"--namespace", "platform-components",
		"--set", "bootstrap.install=platform",
		"--set", "bootstrap.domain=default.localhost",
		"--set", "bootstrap.spiffeTrustDomain=default.k8s.localhost",
		"--set-string", "bootstrap.secrets.localFakeVault.address=https://10.0.2.15:19443/vault/default",
		"--set-string", "bootstrap.secrets.localFakeVault.caCert=bG9jYWwtY2E=",
	)
	for _, required := range []string{
		"chart: ./charts/platform-components\n      reconcileStrategy: Revision",
		"podplane-operator:\n            enabled: true",
		"spiffe:\n                      trustDomain: \"default.k8s.localhost\"",
		"secrets:\n                    defaultProvider: local-fakevault",
		"local-fakevault:\n                        kind: openbao",
		"name: podplane-secrets-provider-ca-local-fakevault",
		"mountPath: /var/run/podplane/secrets-providers/local-fakevault",
		"envoy-gateway:\n            platform:\n              envoyGateway:\n                ingress:\n                  enabled: true",
		"- apex: \"default.localhost\"",
		"certificates:\n                    provider: node",
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("platform local bootstrap is missing %q", required)
		}
	}
	if strings.Contains(rendered, "ingressCertificates:") {
		t.Error("local bootstrap must not enable operator ingress certificate issuance")
	}
}

// TestVaultWorkloadCertificateContract verifies the Vault workload key mount.
func TestVaultWorkloadCertificateContract(t *testing.T) {
	rendered := render(t, "../charts/podplane-operator", "--namespace", "platform-podplane-operator",
		"--set", "podplane.operator.config.cluster.id=prod",
		"--set", "podplane.operator.config.cluster.spiffe.trustDomain=k8s.example.com",
		"--set", "podplane.operator.config.secrets.defaultProvider=vault",
		"--set", "podplane.operator.config.secrets.providers.vault.kind=vault",
		"--set", "podplane.operator.config.secrets.providers.vault.address=https://vault.example",
		"--set", "podplane.operator.config.secrets.providers.vault.mountPath=platform",
		"--set", "podplane.operator.config.secrets.providers.vault.authPath=auth/podplane",
		"--set", "podplane.operator.config.secrets.providers.vault.caCert=private-ca")
	for _, required := range []string{
		"provider: vault",
		`vaultAddress: "https://vault.example"`,
		`vaultAuthMountPath: "podplane"`,
		`vaultCACertPath: "/var/run/podplane/secrets-providers/vault/ca.crt"`,
		`roleName: "podplane-operator"`,
		"objectName: workload-ca-key",
		`secretPath: "platform/data/prod/workload-ca-key"`,
		"secretKey: value",
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("Vault workload certificate render is missing %q", required)
		}
	}
}

// TestWorkloadCertificateGatewayTrustContract verifies direct Gateway API workload trust.
func TestWorkloadCertificateGatewayTrustContract(t *testing.T) {
	rendered := render(t, "../charts/podplane-operator",
		"--namespace", "platform-podplane-operator", "-f", "../tests/values/podplane-operator.yaml",
		"--set", "podplane.operator.config.cluster.spiffe.trustDomain=cluster.example",
		"--set", "podplane.operator.config.cluster.oidc.issuerURL=https://issuer.example",
		"--set", "podplane.operator.config.registry.auth.enabled=true",
	)
	for _, required := range []string{
		"kind: ClusterTrustBundle", "name: certificates.podplane.dev:workload:roots",
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("Gateway trust render is missing %q", required)
		}
	}
	for _, forbidden := range []string{"workload-ca-bundle", "platform-podplane-workload-bundle", "ca_bundle_mirror"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("Gateway trust render contains obsolete mirror reference %q", forbidden)
		}
	}
}

// TestWorkloadCertificateOrdering verifies workload certificate runtime dependencies.
func TestWorkloadCertificateOrdering(t *testing.T) {
	rendered := render(t, "../charts/platform-components",
		"--namespace", "platform-components",
		"--set", "platform.components.apps.podplane-operator.enabled=true",
		"--set", "platform.components.values.podplane-operator.podplane.operator.config.secrets.defaultProvider=google",
		"--set", "platform.components.values.podplane-operator.podplane.operator.config.secrets.providers.google.kind=gcp",
	)
	operatorStart := strings.Index(rendered, "name: podplane-operator\n")
	if operatorStart < 0 {
		t.Fatal("operator HelmRelease not rendered")
	}
	operator := rendered[operatorStart:]
	for _, dependency := range []string{"name: secrets-store-csi-driver", "name: secrets-store-csi-provider-gcp"} {
		if !strings.Contains(operator, dependency) {
			t.Errorf("operator ordering is missing %q", dependency)
		}
	}
}

// TestVaultWorkloadCertificateOrdering verifies a Vault-backed operator waits
// for the Vault CSI provider.
func TestVaultWorkloadCertificateOrdering(t *testing.T) {
	rendered := render(t, "../charts/platform-components",
		"--namespace", "platform-components",
		"--set", "platform.components.apps.podplane-operator.enabled=true",
		"--set", "platform.components.values.podplane-operator.podplane.operator.config.secrets.defaultProvider=vault",
		"--set", "platform.components.values.podplane-operator.podplane.operator.config.secrets.providers.vault.kind=vault",
	)
	operatorStart := strings.Index(rendered, "name: podplane-operator\n")
	if operatorStart < 0 {
		t.Fatal("operator HelmRelease not rendered")
	}
	if !strings.Contains(rendered[operatorStart:], "name: secrets-store-csi-provider-vault") {
		t.Fatal("operator ordering is missing the Vault CSI provider")
	}
}
