// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestZotRegistryIngress checks that registry and native token requests share the Zot backend.
func TestZotRegistryIngress(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		args := []string{"-f", "values/zot-registry.yaml", "--set", "platform.zotRegistry.registryHostname=registry.example.com"}
		if enabled {
			args = append(args, "--set", "zot.httproute.enabled=true",
				"--set", "zot.httproute.parentRefs[0].name=platform-envoy-gateway",
				"--set", "zot.httproute.parentRefs[0].namespace=platform-envoy-gateway",
				"--set", "zot.httproute.parentRefs[0].sectionName=https-wildcard-example-com-a379a6f6",
				"--set", "zot.httproute.hostnames[0]=registry.example.com")
		}
		var route, config map[string]any
		for _, doc := range chartDocuments(t, render(t, "../charts/zot-registry", args...)) {
			if doc["kind"] == "HTTPRoute" {
				route = chartMap(doc, "spec")
			}
			if doc["kind"] == "ConfigMap" && chartMap(doc, "metadata")["name"] == "zot-registry-config" {
				if err := json.Unmarshal([]byte(chartMap(doc, "data")["config.json"].(string)), &config); err != nil {
					t.Fatal(err)
				}
			}
		}
		bearer := chartMap(config, "http", "auth", "bearer")
		if bearer["realm"] != "https://registry.example.com/zot/auth/token" || bearer["service"] != "registry.example.com" {
			t.Fatalf("incorrect registry challenge: %v", bearer)
		}
		if (route != nil) != enabled {
			t.Fatalf("HTTPRoute present = %v, want %v", route != nil, enabled)
		}
		if !enabled {
			continue
		}
		parent := route["parentRefs"].([]any)[0].(map[string]any)
		if parent["sectionName"] != "https-wildcard-example-com-a379a6f6" || parent["namespace"] != "platform-envoy-gateway" {
			t.Fatalf("incorrect HTTPS attachment: %v", parent)
		}
		rule := route["rules"].([]any)[0].(map[string]any)
		want := []any{
			map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/v2"}},
			map[string]any{"path": map[string]any{"type": "Exact", "value": "/zot/auth/token"}},
		}
		if !reflect.DeepEqual(rule["matches"], want) {
			t.Fatalf("route matches = %v, want %v", rule["matches"], want)
		}
		backend := rule["backendRefs"].([]any)[0].(map[string]any)
		if backend["name"] != "zot-registry" || backend["port"] != 5000 {
			t.Fatalf("registry and token paths must both reach Zot: %v", backend)
		}
	}
}

// TestZotGoogleADC ensures GCS uses ADC without a service-account key or CSI mount.
func TestZotGoogleADC(t *testing.T) {
	found := false
	for _, doc := range chartDocuments(t, render(t, "../charts/zot-registry", "-f", "values/zot-registry.yaml", "--set", "platform.zotRegistry.storage.driver=gcs")) {
		if doc["kind"] == "SecretProviderClass" {
			t.Fatal("Zot must not mount a Google key through CSI")
		}
		if doc["kind"] != "ConfigMap" || chartMap(doc, "metadata")["name"] != "zot-registry-config" {
			continue
		}
		found = true
		var config map[string]any
		if err := json.Unmarshal([]byte(chartMap(doc, "data")["config.json"].(string)), &config); err != nil {
			t.Fatal(err)
		}
		driver := chartMap(config, "storage", "storageDriver")
		if driver["name"] != "gcs" || driver["rootdirectory"] != "//" || driver["keyfile"] != nil || driver["credentials"] != nil {
			t.Fatalf("unexpected GCS configuration: %v", driver)
		}
	}
	if !found {
		t.Fatal("missing Zot configuration")
	}
}
