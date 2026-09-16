// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"strings"
	"testing"
)

const workloadInjectionAnnotation = "certificates.podplane.dev/inject-ca-from: workload"

// TestOperatorServingCertificateContract verifies operator serving certificate provisioning.
func TestOperatorServingCertificateContract(t *testing.T) {
	rendered := render(t, "../charts/podplane-operator",
		"--namespace", "platform-podplane-operator", "-f", "../tests/values/podplane-operator.yaml",
		"--set", "podplane.operator.config.registry.auth.enabled=true",
		"--set", "podplane.operator.config.cluster.oidc.issuerURL=https://issuer.example",
		"--set", "podplane.operator.config.cluster.oidc.clientID=registry",
	)
	for _, required := range []string{
		workloadInjectionAnnotation,
		"--serving-namespace=platform-podplane-operator",
		"--aggregated-api-tls-cert-file=/var/run/podplane/serving/aggregated-api/tls.crt",
		"--aggregated-api-tls-private-key-file=/var/run/podplane/serving/aggregated-api/tls.key",
		"--aggregated-api-service-name=platform-podplane-operator-aggregated-api",
		"--registry-auth-tls-cert-file=/var/run/podplane/serving/registry-auth/tls.crt",
		"--registry-auth-tls-private-key-file=/var/run/podplane/serving/registry-auth/tls.key",
		"--registry-auth-service-name=platform-podplane-operator-registry-auth",
		"name: aggregated-api-serving-tls", "name: registry-auth-serving-tls", "emptyDir: {}",
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("operator render is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"apiVersion: cert-manager.io/", "kind: Certificate", "secretName: platform-podplane-operator-tls", "cert-manager.io/inject-ca-from",
		"--aggregated-api-tls-dns-names", "--registry-auth-tls-dns-names",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("operator render unexpectedly contains %q", forbidden)
		}
	}
}

// TestOperatorIngressCertificateConfig verifies ingress certificate values map to operator config.
func TestOperatorIngressCertificateConfig(t *testing.T) {
	rendered := render(t, "../charts/podplane-operator",
		"--namespace", "platform-podplane-operator", "-f", "../tests/values/podplane-operator.yaml",
	)
	for _, required := range []string{
		`"ingress_certificates": {`,
		`"default_provider": "aws"`,
		`"server": "https://acme.example/directory"`,
		`"example.com": {`,
		`"internal.example.com": {}`,
		`"kind": "aws-route53"`,
		`"hosted_zone_id": "Z123"`,
		`"role_arn": "arn:aws:iam::123:role/acme"`,
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("operator ingress certificate config is missing %q", required)
		}
	}
}

// TestWorkloadCAInjectorRBAC verifies injection access covers supported API extensions.
func TestWorkloadCAInjectorRBAC(t *testing.T) {
	rendered := render(t, "../charts/podplane-operator",
		"--namespace", "platform-podplane-operator", "-f", "../tests/values/podplane-operator.yaml",
		"--set", "podplane.operator.config.cluster.spiffe.trustDomain=cluster.example",
	)
	for _, required := range []string{
		"resources: [\"apiservices\"]\n    verbs: [\"get\", \"list\", \"patch\"]",
		"resources: [\"mutatingwebhookconfigurations\"]\n    verbs: [\"get\", \"list\", \"patch\"]",
		"resources: [\"validatingwebhookconfigurations\"]\n    verbs: [\"get\", \"list\", \"patch\"]",
		"resources: [\"customresourcedefinitions\"]\n    verbs: [\"get\", \"list\", \"patch\"]",
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("CA injector RBAC is missing %q", required)
		}
	}
}

// TestCAPIWorkloadCertificateContract verifies Cluster API uses workload certificates.
func TestCAPIWorkloadCertificateContract(t *testing.T) {
	rendered := render(t, "../charts/cluster-api", "--namespace", "platform-cluster-api")
	for _, required := range []string{
		"podCertificate:", "signerName: certificates.podplane.dev/workload", "keyType: ED25519",
		"keyPath: tls.key", "certificateChainPath: tls.crt", "userAnnotations:",
		"certificates.podplane.dev/mode: service", "certificates.podplane.dev/service: capi-webhook-service",
		workloadInjectionAnnotation,
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("CAPI render is missing %q", required)
		}
	}
	for _, forbidden := range []string{"apiVersion: cert-manager.io/", "kind: Certificate", "kind: Issuer", "capi-webhook-service-cert", "cert-manager.io/inject-ca-from"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("CAPI render unexpectedly contains %q", forbidden)
		}
	}

	crds := render(t, "../charts/cluster-api-crds")
	if strings.Contains(crds, "cert-manager.io/inject-ca-from") || !strings.Contains(crds, workloadInjectionAnnotation) {
		t.Error("CAPI CRDs must use the Podplane workload CA injection annotation")
	}
}

// TestNstanceWorkloadCertificateContract verifies Nstance uses workload certificates.
func TestNstanceWorkloadCertificateContract(t *testing.T) {
	rendered := render(t, "../charts/nstance-operator",
		"--namespace", "platform-nstance-operator", "-f", "../tests/values/nstance-operator.yaml",
		"--set", "fullnameOverride=platform-nstance-operator",
	)
	for _, required := range []string{
		"podCertificate:", "signerName: certificates.podplane.dev/workload", "keyType: ED25519",
		"keyPath: tls.key", "certificateChainPath: tls.crt", "userAnnotations:",
		"certificates.podplane.dev/mode: service", "certificates.podplane.dev/service: platform-nstance-operator-webhook",
		workloadInjectionAnnotation,
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("Nstance render is missing %q", required)
		}
	}
	for _, forbidden := range []string{"apiVersion: cert-manager.io/", "kind: Certificate", "kind: Issuer", "cert-manager.io/inject-ca-from", "secretName:"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("Nstance render unexpectedly contains %q", forbidden)
		}
	}
}
