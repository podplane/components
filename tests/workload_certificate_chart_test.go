// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"strings"
	"testing"
)

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
		"\"trust_domain\": \"cluster.example\"", "\"certificates\":", "\"ca_path\": \"/var/run/podplane/certificates/workload-ca-key.pem\"",
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
		`secretPath: "secret/data/local/workload-ca-key"`,
		"secretKey: value",
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("OpenBao workload certificate render is missing %q", required)
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
