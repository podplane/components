// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"encoding/json"
	"testing"
)

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
