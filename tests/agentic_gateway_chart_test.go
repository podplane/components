// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSourceGatewayContract checks workload TLS and encryption-key binding wiring,
// always using the cluster default provider.
func TestSourceGatewayContract(t *testing.T) {
	rendered := render(t, "../charts/sourcegateway", "--namespace", "platform-sourcegateway")
	// gVisor cannot join a Pod user namespace, so Source Gateway must run non-root
	// without one rather than as UID 0 with hostUsers=false.
	for _, want := range []string{"hostUsers: true", "runAsUser: 65532", "fsGroup: 65532", "serviceAccountName: sourcegateway", "secretProviderClass: \"sourcegateway\"", "signerName: certificates.podplane.dev/workload", "keyType: ED25519", "keyPath: tls.key", "certificateChainPath: tls.crt", "certificates.podplane.dev/mode: service", "certificates.podplane.dev/service: sourcegateway"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(rendered, "runAsUser: 0\n") {
		t.Error("Source Gateway must not run as UID 0 without a user namespace")
	}
	bindings, pvcs := 0, 0
	for _, doc := range chartDocuments(t, rendered) {
		switch doc["kind"] {
		case "SecretProviderClass", "CustomResourceDefinition":
			t.Errorf("unexpected resource %s", doc["kind"])
		case "PersistentVolumeClaim":
			pvcs++
			spec := chartMap(doc, "spec")
			if spec["storageClassName"] != nil || !reflect.DeepEqual(spec["accessModes"], []any{"ReadWriteOnce"}) || chartMap(spec, "resources", "requests")["storage"] != "30Gi" {
				t.Fatal("Source Gateway must use the default class with upstream capacity and access mode", spec)
			}
		case "SecretProviderBinding":
			bindings++
			if doc["apiVersion"] != "secrets.podplane.dev/v1beta1" || chartMap(doc, "metadata")["name"] != "sourcegateway" || chartMap(doc, "metadata")["namespace"] != "platform-sourcegateway" {
				t.Fatalf("incorrect binding: %v", doc)
			}
			if _, ok := chartMap(doc, "spec")["providerName"]; ok {
				t.Fatal("default binding must omit providerName")
			}
			items := chartMap(doc, "spec")["items"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["key"] != "encryption-key" || items[0].(map[string]any)["path"] != "encryption-key" {
				t.Fatalf("incorrect binding items: %v", items)
			}
		}
	}
	if bindings != 1 || pvcs != 1 {
		t.Fatalf("got %d bindings and %d PVCs, want one of each", bindings, pvcs)
	}
	override := render(t, "../charts/sourcegateway", "--set", "platform.sourceGateway.providerName=alternate")
	for _, doc := range chartDocuments(t, override) {
		if doc["kind"] == "SecretProviderBinding" {
			if _, ok := chartMap(doc, "spec")["providerName"]; ok {
				t.Fatal("obsolete provider override must not replace the cluster default")
			}
		}
	}
}

// TestAgentRouterContract checks that the upstream chart patch uses Podplane
// workload TLS and CA injection without generating a separate serving secret.
func TestAgentRouterContract(t *testing.T) {
	rendered := render(t, "../charts/agent-router", "--namespace", "platform-agent-router")
	for _, want := range []string{"signerName: certificates.podplane.dev/workload", "certificates.podplane.dev/service: agent-router", "name: certificates.podplane.dev:workload:roots", "path: ca.crt"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q", want)
		}
	}
	secrets, webhooks := 0, 0
	for _, doc := range chartDocuments(t, rendered) {
		switch doc["kind"] {
		case "GatewayClass", "CustomResourceDefinition", "Certificate", "Issuer":
			t.Errorf("unexpected %s", doc["kind"])
		case "Secret":
			secrets++
			if chartMap(doc, "metadata")["name"] != "agent-router-mcp-session-encryption" {
				t.Fatalf("unexpected Secret: %v", chartMap(doc, "metadata"))
			}
		case "MutatingWebhookConfiguration":
			webhooks++
			if chartMap(doc, "metadata")["name"] != "envoy-ai-gateway-gateway-pod-mutator.platform-agent-router" || chartMap(doc, "metadata", "annotations")["certificates.podplane.dev/inject-ca-from"] != "workload" {
				t.Errorf("incorrect workload trust injection: %v", doc)
			}
			webhook := doc["webhooks"].([]any)[0].(map[string]any)
			if chartMap(webhook, "clientConfig")["caBundle"] != nil || chartMap(webhook, "clientConfig", "service")["name"] != "agent-router" {
				t.Errorf("incorrect webhook: %v", webhook)
			}
		}
	}
	if secrets != 1 || webhooks != 1 {
		t.Fatalf("got %d Secrets and %d webhooks", secrets, webhooks)
	}
	// AI/MCP routes get their own internal proxy fleet, separate from the
	// ingress DaemonSet and its host ports.
	for _, want := range []string{"kind: Gateway\n", "gatewayClassName: platform-envoy-gateway", "mergeType: Replace", "envoyDeployment:", "image: \"docker.io/envoyproxy/envoy:distroless-v1.39.1\"", "type: ClusterIP", "name: platform-agent-router-extproc-gvisor-map", "reinvocationPolicy: IfNeeded"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, forbidden := range []string{"envoyDaemonSet:", "hostPort:"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("unexpected %q", forbidden)
		}
	}
}

// TestAgenticGatewayPlatformContract checks gateway release wiring and image
// mirrors, and that enabling Agent Router preserves Envoy's SDS configuration
// while disabling it removes the extension dependency.
func TestAgenticGatewayPlatformContract(t *testing.T) {
	rendered := render(t, "../charts/platform-components", "-f", "values/platform-components.yaml", "--set", "platform.components.apps.sourcegateway.enabled=true,platform.components.apps.agent-router.enabled=true,platform.components.apps.envoy-gateway.enabled=true")
	for _, doc := range chartDocuments(t, rendered) {
		name := chartMap(doc, "metadata")["name"]
		if doc["kind"] == "Namespace" && (name == "platform-sourcegateway" || name == "platform-agent-router") {
			labels := chartMap(doc, "metadata", "labels")
			if labels["pod-security.kubernetes.io/enforce"] != "restricted" || labels["runtime.podplane.dev/runc"] != nil {
				t.Errorf("unsafe namespace %v", doc)
			}
		}
		if doc["kind"] != "HelmRelease" {
			continue
		}
		spec := chartMap(doc, "spec")
		switch name {
		case "sourcegateway":
			deps := map[string]string{}
			for _, dep := range spec["dependsOn"].([]any) {
				entry := dep.(map[string]any)
				deps[entry["name"].(string)] = entry["namespace"].(string)
			}
			for _, dep := range []string{"sourcegateway-crds", "podplane-operator", "secrets-store-csi-driver"} {
				if deps[dep] == "" {
					t.Errorf("missing dependency %s", dep)
				}
			}
			image := chartMap(spec, "values", "sourcegateway", "image")
			if image["repository"] != "registry.example/mirror/ghcr.io/sourcegateway/sourcegateway" {
				t.Errorf("source mirror %v", image)
			}
		case "agent-router":
			values := chartMap(spec, "values", "ai-gateway-helm")
			for part, suffix := range map[string]string{"controller": "controller", "extProc": "extproc"} {
				if chartMap(values, part, "image")["repository"] != "registry.example/mirror/docker.io/envoyproxy/ai-gateway-"+suffix {
					t.Errorf("agent mirror %v", values)
				}
			}
		case "envoy-gateway":
			config := chartMap(spec, "values", "gateway-helm", "config", "envoyGateway")
			if chartMap(config, "extensionApis")["enableBackend"] != true || chartMap(config, "extensionManager", "service", "fqdn")["hostname"] != "agent-router.platform-agent-router.svc.cluster.local" {
				t.Errorf("Agent Router extension missing: %v", config)
			}
			values, err := yaml.Marshal(spec["values"])
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "values.yaml")
			if err := os.WriteFile(file, values, 0o600); err != nil {
				t.Fatal(err)
			}
			gateway := render(t, "../charts/envoy-gateway", "-f", file)
			for _, want := range []string{"enableSDSSecretRef: true", "enableBackend: true", "includeAll: true", "agent-router.platform-agent-router.svc.cluster.local", "type: GatewayNamespace", "signerName: certificates.podplane.dev/workload"} {
				if !strings.Contains(gateway, want) {
					t.Errorf("composed gateway configuration lost %q", want)
				}
			}
		}
	}
	without := render(t, "../charts/platform-components", "-f", "values/platform-components.yaml", "--set", "platform.components.apps.envoy-gateway.enabled=true")
	if strings.Contains(without, "extensionManager") || strings.Contains(without, "name: agent-router") {
		t.Fatal("platform without Agent Router must not depend on its extension")
	}
}
