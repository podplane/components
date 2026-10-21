// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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
}

// TestAgenticGatewayPlatformContract checks gateway release wiring and image
// mirrors, and that enabling Agent Router preserves Envoy's SDS configuration
// while disabling it removes the extension dependency.
func TestAgenticGatewayPlatformContract(t *testing.T) {
	rendered := render(t, "../charts/platform-components", "-f", "values/platform-components.yaml", "--set", "platform.components.apps.agent-router.enabled=true,platform.components.apps.envoy-gateway.enabled=true")
	for _, doc := range chartDocuments(t, rendered) {
		name := chartMap(doc, "metadata")["name"]
		if doc["kind"] == "Namespace" && name == "platform-agent-router" {
			labels := chartMap(doc, "metadata", "labels")
			want := "restricted"
			if labels["pod-security.kubernetes.io/enforce"] != want || labels["runtime.podplane.dev/runc"] != nil {
				t.Errorf("unsafe namespace %v", doc)
			}
		}
		if doc["kind"] != "HelmRelease" {
			continue
		}
		spec := chartMap(doc, "spec")
		switch name {
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
