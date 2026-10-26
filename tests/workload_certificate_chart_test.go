// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// TestOperatorTrustBundleSync verifies config mapping and namespace-scoped permissions.
func TestOperatorTrustBundleSync(t *testing.T) {
	const resources = `[
		{"kind":"Secret","namespace":"consumer-a","name":"roots","key":"ca.crt","createIfMissing":false},
		{"kind":"ConfigMap","namespace":"consumer-a","name":"roots","key":"ca.crt"},
		{"kind":"ConfigMap","namespace":"consumer-a","name":"roots","key":"other.crt","createIfMissing":false},
		{"kind":"ConfigMap","namespace":"consumer-a","name":"other.roots","key":"CA_bundle-1"},
		{"kind":"Secret","namespace":"consumer-b","name":"roots","key":"ca.crt","createIfMissing":true},
		{"kind":"ConfigMap","namespace":"consumer-c","name":"roots","key":"ca.crt","createIfMissing":false}
	]`
	for _, tc := range []struct {
		name      string
		resources string
		create    bool
		remove    string
	}{
		{"default", "[]", false, ""},
		{"empty with creation enabled", "[]", true, ""},
		{"creation opt-in", resources, false, ""},
		{"creation opt-out", resources, true, ""},
		{"null removes global boolean", "[]", false, "createIfMissing"},
		{"null removes resources", "[]", false, "resources"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"--namespace", "operator-system", "-f", "values/podplane-operator.yaml",
				"--set", "podplane.operator.namePrefix=custom-operator,podplane.operator.serviceAccountName=custom-sa"}
			if tc.name != "default" {
				args = append(args, "--set-json", fmt.Sprintf("podplane.operator.config.workloadCertificates.trustBundleSync={\"createIfMissing\":%t,\"resources\":%s}", tc.create, tc.resources))
			}
			if tc.remove != "" {
				// Helm coalescing deletes null-valued default keys before rendering.
				args = append(args, "--set-json", "podplane.operator.config.workloadCertificates.trustBundleSync."+tc.remove+"=null")
			}
			docs := chartDocuments(t, render(t, "../charts/podplane-operator", args...))
			var config map[string]any
			roles, bindings := map[string]any{}, map[string]any{}
			for _, doc := range docs {
				metadata := chartMap(doc, "metadata")
				switch doc["kind"] {
				case "ConfigMap":
					if raw, ok := chartMap(doc, "data")["config.json"].(string); ok {
						if err := json.Unmarshal([]byte(raw), &config); err != nil {
							t.Fatal(err)
						}
					} else {
						t.Fatal("chart must not provision trust-bundle destinations")
					}
				case "Secret":
					t.Fatal("chart must not provision trust-bundle destinations")
				case "ClusterRole":
					for _, raw := range doc["rules"].([]any) {
						for _, resource := range raw.(map[string]any)["resources"].([]any) {
							if resource == "secrets" || resource == "configmaps" || resource == "*" {
								t.Fatalf("cluster-wide trust destination access: %v", raw)
							}
						}
					}
				case "Role", "RoleBinding":
					if metadata["name"] != "custom-operator-trust-bundle-sync" {
						continue
					}
					namespace := metadata["namespace"].(string)
					if doc["kind"] == "Role" {
						if _, exists := roles[namespace]; exists {
							t.Fatalf("duplicate Role in %s", namespace)
						}
						roles[namespace] = doc["rules"]
					} else {
						if _, exists := bindings[namespace]; exists {
							t.Fatalf("duplicate RoleBinding in %s", namespace)
						}
						bindings[namespace] = true
						wantRef := map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "custom-operator-trust-bundle-sync"}
						wantSubjects := []any{map[string]any{"kind": "ServiceAccount", "name": "custom-sa", "namespace": "operator-system"}}
						if !reflect.DeepEqual(doc["roleRef"], wantRef) || !reflect.DeepEqual(doc["subjects"], wantSubjects) {
							t.Fatalf("incorrect binding: %v", doc)
						}
					}
				}
			}
			var wantSync map[string]any
			wantJSON := fmt.Sprintf(`{"create_if_missing":%t,"resources":%s}`, tc.create, strings.ReplaceAll(tc.resources, "createIfMissing", "create_if_missing"))
			if err := json.Unmarshal([]byte(wantJSON), &wantSync); err != nil {
				t.Fatal(err)
			}
			if got := chartMap(config, "certificates", "trust_bundle_sync"); !reflect.DeepEqual(got, wantSync) {
				t.Fatalf("trust bundle config = %v, want %v", got, wantSync)
			}
			wantRoles, wantBindings := map[string]any{}, map[string]any{}
			if tc.resources != "[]" {
				// Exact rules reject broad verbs, unnamed reads, and named create grants.
				// rule builds the expected named read/write permission.
				rule := func(kind string, names ...any) map[string]any {
					return map[string]any{"apiGroups": []any{""}, "resources": []any{kind}, "resourceNames": names, "verbs": []any{"get", "patch"}}
				}
				// create builds the expected namespace-scoped creation permission.
				create := func(kind string) map[string]any {
					return map[string]any{"apiGroups": []any{""}, "resources": []any{kind}, "verbs": []any{"create"}}
				}
				a := []any{rule("configmaps", "other.roots", "roots")}
				if tc.create {
					a = append(a, create("configmaps"))
				}
				wantRoles["consumer-a"] = append(a, rule("secrets", "roots"))
				wantRoles["consumer-b"] = []any{rule("secrets", "roots"), create("secrets")}
				wantRoles["consumer-c"] = []any{rule("configmaps", "roots")}
				wantBindings = map[string]any{"consumer-a": true, "consumer-b": true, "consumer-c": true}
			}
			if !reflect.DeepEqual(roles, wantRoles) || !reflect.DeepEqual(bindings, wantBindings) {
				t.Fatalf("RBAC mismatch:\nroles: %v\nwant: %v\nbindings: %v\nwant: %v", roles, wantRoles, bindings, wantBindings)
			}
		})
	}
}

// TestOperatorTrustBundleSyncValidation rejects malformed or duplicate destinations.
func TestOperatorTrustBundleSyncValidation(t *testing.T) {
	const target = `{"kind":"Secret","namespace":"consumer","name":"roots","key":"ca.crt"}`
	cases := map[string]string{
		"object required":         `[]`,
		"global boolean":          `{"createIfMissing":"false"}`,
		"resource list":           `{"resources":{}}`,
		"resource object":         `{"resources":["Secret"]}`,
		"null resource":           `{"resources":[null]}`,
		"duplicate":               `{"resources":[` + target + `,` + target + `]}`,
		"duplicate with override": `{"resources":[` + target + `,` + strings.TrimSuffix(target, "}") + `,"createIfMissing":true}]}`,
	}
	for name, replacement := range map[string]string{
		"unsupported kind": `"kind":"Service"`,
		"lowercase kind":   `"kind":"secret"`,
		"missing kind":     `"other":"Secret"`,
		"numeric kind":     `"kind":123`,
	} {
		cases[name] = `{"resources":[` + strings.Replace(target, `"kind":"Secret"`, replacement, 1) + `]}`
	}
	for field, invalid := range map[string][]any{
		"namespace":       {"", "Upper", "a.b", "a/b", "-bad", strings.Repeat("a", 64), 123, nil},
		"name":            {"", "Upper", "a/b", "a..b", "bad-", strings.Repeat("a", 254), 123, nil},
		"key":             {"", "a/b", "a b", ".", "..", "..hidden", strings.Repeat("a", 254), 123, nil},
		"createIfMissing": {"false", 0, nil},
	} {
		for i, value := range invalid {
			var resource map[string]any
			if err := json.Unmarshal([]byte(target), &resource); err != nil {
				t.Fatal(err)
			}
			resource[field] = value
			raw, err := json.Marshal(map[string]any{"resources": []any{resource}})
			if err != nil {
				t.Fatal(err)
			}
			cases[fmt.Sprintf("invalid %s %d", field, i)] = string(raw)
		}
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := exec.Command("helm", "template", "test", "../charts/podplane-operator", "--kube-version", "1.37.0",
				"-f", "values/podplane-operator.yaml", "--set-json", "podplane.operator.config.workloadCertificates.trustBundleSync="+value).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "trustBundleSync") {
				t.Fatalf("expected trustBundleSync validation error, got %v\n%s", err, out)
			}
		})
	}
}

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
		"\"trust_domain\": \"cluster.example\"", "\"certificates\":", "\"ca_path\": \"/var/run/podplane/certificates/workload-ca-key\"",
	} {
		if !strings.Contains(enabled, required) {
			t.Errorf("enabled render is missing %q", required)
		}
	}
	for _, forbidden := range []string{"kind: Secret\n", "kind: ValidatingAdmissionPolicy", "Podplane Workload CA", "providerCredentials", "helm.sh/hook", "subPath:", "\"workload_certificates\""} {
		if strings.Contains(enabled, forbidden) {
			t.Errorf("workload contract unexpectedly contains %q", forbidden)
		}
	}
	policies := render(t, "../charts/platform-rbac", "--namespace", "platform-cluster")
	for _, required := range []string{"platform-reserved-workload-ca-spc-vap", "object.metadata.namespace == 'platform-podplane-operator'", "object.metadata.name == 'platform-podplane-operator'"} {
		if !strings.Contains(policies, required) {
			t.Errorf("platform workload CA protection is missing %q", required)
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
