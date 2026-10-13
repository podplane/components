// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// TestEnvoyGatewayFinalNodeScheduling checks the data-plane scheduling contract,
// not merely the gateway controller Deployment's tolerations.
func TestEnvoyGatewayFinalNodeScheduling(t *testing.T) {
	docs := envoyIngressDocuments(t)
	var proxy map[string]any
	for _, doc := range docs {
		if doc["kind"] == "EnvoyProxy" {
			proxy = doc
		}
	}
	if proxy == nil {
		t.Fatal("ingress must render an EnvoyProxy")
	}
	kubernetes := chartMap(proxy, "spec", "provider", "kubernetes")
	if kubernetes["envoyDeployment"] != nil {
		t.Error("data plane must use a DaemonSet, not a Deployment")
	}
	ds := chartMap(kubernetes, "envoyDaemonSet")
	if ds == nil {
		t.Fatal("missing envoyDaemonSet")
	}
	patch := chartMap(ds, "patch", "value", "spec", "template", "spec")
	pod := chartMap(ds, "pod")
	for _, key := range []string{"node-role.kubernetes.io/control-plane", "node-role.kubernetes.io/master"} {
		t.Run(key, func(t *testing.T) {
			// Tolerations can be supplied through Envoy's Pod API or the DS patch.
			tolerated := false
			for _, spec := range []map[string]any{pod, patch} {
				entries, _ := spec["tolerations"].([]any)
				for _, entry := range entries {
					tol, _ := entry.(map[string]any)
					if (tol["effect"] == nil || tol["effect"] == "" || tol["effect"] == "NoSchedule") &&
						tol["operator"] == "Exists" && (tol["key"] == key || tol["key"] == nil || tol["key"] == "") &&
						tol["tolerationSeconds"] == nil {
						tolerated = true
					}
				}
			}
			if !tolerated {
				t.Errorf("Envoy DaemonSet lacks an indefinite Exists toleration for %s:NoSchedule; final tainted control-plane node is not covered", key)
			}
		})
	}
	// Keep the default topology unconstrained: no worker selector, required
	// anti-affinity, or minimum-domain spread that needs another node.
	for _, spec := range []map[string]any{pod, patch} {
		for _, field := range []string{"nodeSelector", "affinity", "topologySpreadConstraints", "nodeName"} {
			if value := spec[field]; value != nil {
				t.Errorf("default Envoy DaemonSet must remain one-node compatible: unexpected %s=%v", field, value)
			}
		}
	}
}

func TestEnvoyGatewayDoesNotRouteKubeAPIServer(t *testing.T) {
	for _, doc := range envoyIngressDocuments(t) {
		// Inspect all rendered resources, including Gateway listeners, route
		// backends, Service ports, and the EnvoyProxy's patched container ports.
		var check func(any)
		check = func(value any) {
			switch value := value.(type) {
			case map[string]any:
				for key, child := range value {
					if key == "port" || key == "hostPort" || key == "containerPort" || key == "targetPort" {
						if child == 6443 || child == "6443" {
							t.Errorf("%s contains kube-apiserver port in %s", doc["kind"], key)
						}
					}
					if key == "backendRefs" {
						refs, _ := child.([]any)
						for _, ref := range refs {
							backend, _ := ref.(map[string]any)
							if backend["name"] == "kubernetes" || backend["name"] == "kube-apiserver" {
								t.Errorf("%s routes to the Kubernetes API service", doc["kind"])
							}
						}
					}
					check(child)
				}
			case []any:
				for _, child := range value {
					check(child)
				}
			}
		}
		check(doc)
	}
}

func envoyIngressDocuments(t *testing.T) []map[string]any {
	t.Helper()
	rendered := render(t, "../charts/envoy-gateway", "--namespace", "platform-envoy-gateway",
		"--set", "platform.envoyGateway.ingress.enabled=true",
		"--set", "platform.envoyGateway.ingress.certificates.provider=aws",
		"--set", "platform.envoyGateway.ingress.certificates.objectType=secretsmanager",
		"--set", "platform.envoyGateway.ingress.domains[0].apex=example.com")
	return chartDocuments(t, rendered)
}

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
		"name: https-443", "containerPort: 10443", "hostPort: 443", "name: podplane-sds", `image: "docker.io/envoyproxy/envoy:distroless-v1.39.0"`, `image: "ghcr.io/podplane/operator:v0.8.0"`, "name: podplane-sds\n                      emptyDir:\n                        medium: Memory",
		"name: certs\n                      configMap: null\n                      projected:", "name: certificates.podplane.dev:workload:roots", "path: ca.crt",
		"kind: ServiceAccount", "name: platform-envoy-gateway-ingress-certificates", "serviceAccountName: platform-envoy-gateway-ingress-certificates",
		"driver: secrets-store.csi.k8s.io", "--socket=/var/run/podplane-sds/sds.sock",
		"mountPath: /var/run/podplane/ingress-certificates", "--domain-certificate=bundle-a379a6f6eeafb9a55e378c11=example.com",
		"type: gateway.envoyproxy.io/sds", "url: unix:///var/run/podplane-sds/sds.sock",
		"gatewayClassName: platform-envoy-gateway", `hostname: "example.com"`, `hostname: "*.example.com"`, "mode: Terminate",
		"name: platform-http-to-https-redirect-httproute", "type: RequestRedirect", "scheme: https",
		`objectName: "/clusters/platform-cluster/ingress-certificates/bundle-a379a6f6eeafb9a55e378c11"`,
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("Envoy Gateway render is missing %q", required)
		}
	}
	for _, forbidden := range []string{"--certificates-dir", "tls.crt:", "tls.key:", "kubernetes.io/tls", "cert-manager.io/", "traefik.io/", "platform-acme"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("Envoy Gateway render unexpectedly contains %q", forbidden)
		}
	}
}

// TestEnvoyGatewayNodeCertificateDelivery verifies shared pointers and SDS-only host mounts.
func TestEnvoyGatewayNodeCertificateDelivery(t *testing.T) {
	rendered := render(t, "../charts/envoy-gateway",
		"--set", "platform.envoyGateway.ingress.enabled=true",
		"--set", "platform.envoyGateway.ingress.certificates.provider=node",
		"--set", "platform.envoyGateway.ingress.domains[0].apex=example.com",
		"--set", "platform.envoyGateway.ingress.domains[1].apex=example.org")
	for _, forbidden := range []string{"--certificates-dir", "SecretProviderClass", "secrets-store.csi", "--certificate=", "--domain-certificate=", "bundle-", "DirectoryOrCreate"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("node provider unexpectedly contains %q", forbidden)
		}
	}
	if strings.Count(rendered, "--node-certificate=") != 1 || !strings.Contains(rendered, "--node-certificate=example.com,example.org") {
		t.Error("expected one node certificate argument containing both domains")
	}
	secrets, listeners, mounts := 0, 0, 0
	for _, doc := range chartDocuments(t, rendered) {
		switch doc["kind"] {
		case "Secret":
			secrets++
			data := chartMap(doc, "stringData")
			if chartMap(doc, "metadata")["name"] != "node" || doc["type"] != "gateway.envoyproxy.io/sds" ||
				len(data) != 2 || data["secretName"] != "node" || data["url"] != "unix:///var/run/podplane-sds/sds.sock" || doc["data"] != nil {
				t.Errorf("unexpected SDS pointer: %v", doc)
			}
		case "Gateway":
			for _, entry := range chartMap(doc, "spec")["listeners"].([]any) {
				listener := entry.(map[string]any)
				if listener["protocol"] != "HTTPS" {
					continue
				}
				listeners++
				refs := chartMap(listener, "tls")["certificateRefs"].([]any)
				if len(refs) != 1 || refs[0].(map[string]any)["name"] != "node" {
					t.Errorf("listener must reference shared node pointer: %v", listener)
				}
			}
		case "EnvoyProxy":
			pod := chartMap(doc, "spec", "provider", "kubernetes", "envoyDaemonSet", "patch", "value", "spec", "template", "spec")
			found := false
			for _, entry := range pod["volumes"].([]any) {
				volume := entry.(map[string]any)
				if volume["name"] == "ingress-certificates" {
					found = true
					host := chartMap(volume, "hostPath")
					if host["path"] != "/opt/podplane/secrets/ingress" || host["type"] != "Directory" || volume["csi"] != nil {
						t.Errorf("unexpected ingress volume: %v", volume)
					}
				}
			}
			if !found {
				t.Error("missing node ingress volume")
			}
			for _, entry := range pod["containers"].([]any) {
				container := entry.(map[string]any)
				for _, entry := range container["volumeMounts"].([]any) {
					mount := entry.(map[string]any)
					if mount["name"] == "ingress-certificates" {
						mounts++
						if container["name"] != "podplane-sds" || mount["readOnly"] != true || mount["mountPath"] != "/var/run/podplane/ingress-certificates" {
							t.Errorf("ingress mount must be read-only and SDS-only: %v", container)
						}
					}
				}
			}
		}
	}
	if secrets != 1 || listeners != 4 || mounts != 1 {
		t.Errorf("expected one pointer, four HTTPS listeners, one SDS mount; got %d, %d, %d", secrets, listeners, mounts)
	}
}

// TestEnvoyGatewayNodeComponentOrdering checks node delivery needs no ingress CSI dependency.
func TestEnvoyGatewayNodeComponentOrdering(t *testing.T) {
	rendered := render(t, "../charts/platform-components",
		"--set", "platform.components.apps.envoy-gateway.enabled=true",
		"--set", "platform.components.values.envoy-gateway.platform.envoyGateway.ingress.enabled=true",
		"--set", "platform.components.values.envoy-gateway.platform.envoyGateway.ingress.certificates.provider=node")
	for _, doc := range chartDocuments(t, rendered) {
		if doc["kind"] != "HelmRelease" || chartMap(doc, "metadata")["name"] != "envoy-gateway" {
			continue
		}
		deps := chartMap(doc, "spec")["dependsOn"].([]any)
		if len(deps) != 3 {
			t.Fatalf("node Envoy should depend only on operator and Gateway CRDs: %v", deps)
		}
		for _, entry := range deps {
			if strings.HasPrefix(entry.(map[string]any)["name"].(string), "secrets-store-csi") {
				t.Errorf("node Envoy must not depend on CSI: %v", entry)
			}
		}
		return
	}
	t.Fatal("missing Envoy HelmRelease")
}

// TestEnvoyGatewayInvalidCertificateProvider checks both charts reject unsupported providers.
func TestEnvoyGatewayInvalidCertificateProvider(t *testing.T) {
	for _, chart := range []string{"envoy-gateway", "platform-components"} {
		prefix := "platform.envoyGateway.ingress."
		args := []string{"template", "test", "../charts/" + chart, "--kube-version", "1.37.0"}
		if chart == "platform-components" {
			prefix = "platform.components.values.envoy-gateway." + prefix
			args = append(args, "--set", "platform.components.apps.envoy-gateway.enabled=true")
		}
		args = append(args, "--set", prefix+"enabled=true", "--set", prefix+"domains[0].apex=example.com", "--set", prefix+"certificates.provider=invalid")
		out, err := exec.Command("helm", args...).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "node, aws, gcp, vault, or openbao") {
			t.Errorf("%s should reject invalid provider: %v\n%s", chart, err, out)
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

// TestEnvoyGatewayControlPlaneCertificateProjection verifies private keys remain outside Kubernetes Secrets.
func TestEnvoyGatewayControlPlaneCertificateProjection(t *testing.T) {
	rendered := render(t, "../charts/envoy-gateway", "--namespace", "platform-envoy-gateway")
	for _, required := range []string{
		"podCertificate:",
		"signerName: certificates.podplane.dev/workload",
		"keyType: ECDSAP256",
		"keyPath: tls.key",
		"certificateChainPath: tls.crt",
		"certificates.podplane.dev/mode: service",
		"certificates.podplane.dev/service: envoy-gateway",
		"clusterTrustBundle:",
		"name: certificates.podplane.dev:workload:roots",
		"path: ca.crt",
		"kind: MutatingWebhookConfiguration",
		"certificates.podplane.dev/inject-ca-from: workload",
	} {
		if !strings.Contains(rendered, required) {
			t.Errorf("Envoy Gateway control-plane certificate render is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"secretName: envoy-gateway",
		"envoy-gateway-certgen",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("Envoy Gateway control-plane certificate render unexpectedly contains %q", forbidden)
		}
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
