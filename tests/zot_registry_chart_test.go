// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"encoding/json"
	"os/exec"
	"testing"
)

// TestZotGoogleFederation verifies GCS credential configuration and rejects incompatible federation settings.
func TestZotGoogleFederation(t *testing.T) {
	const provider = "projects/123456/locations/global/workloadIdentityPools/registry/providers/kubernetes"
	for _, federated := range []bool{false, true} {
		name := "ambient ADC"
		args := []string{"-f", "values/zot-registry.yaml", "--set", "platform.zotRegistry.storage.driver=gcs"}
		if federated {
			name = "projected identity"
			args = append(args, "--set", "platform.zotRegistry.storage.workloadIdentityProvider="+provider)
		}
		t.Run(name, func(t *testing.T) {
			var data map[string]any
			for _, doc := range chartDocuments(t, render(t, "../charts/zot-registry", args...)) {
				if doc["kind"] == "ConfigMap" && chartMap(doc, "metadata")["name"] == "zot-registry-config" {
					data = chartMap(doc, "data")
				}
			}
			if data == nil {
				t.Fatal("registry configuration missing")
			}
			var config map[string]any
			if err := json.Unmarshal([]byte(data["config.json"].(string)), &config); err != nil {
				t.Fatal(err)
			}
			driver := chartMap(config, "storage", "storageDriver")
			if driver["name"] != "gcs" || driver["rootdirectory"] != "//" || driver["keyfile"] != nil || driver["credentials"] != nil {
				t.Fatalf("GCS must use ADC and the same bucket-root layout as node readers: %v", driver)
			}
			if !federated {
				if data["credentials.json"] != nil {
					t.Fatal("ambient ADC unexpectedly overridden")
				}
				return
			}
			var credentials map[string]any
			if err := json.Unmarshal([]byte(data["credentials.json"].(string)), &credentials); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{
				"type": "external_account", "audience": "//iam.googleapis.com/" + provider,
				"subject_token_type": "urn:ietf:params:oauth:token-type:jwt", "token_url": "https://sts.googleapis.com/v1/token",
			} {
				if credentials[key] != want {
					t.Errorf("%s = %v, want %s", key, credentials[key], want)
				}
			}
			if chartMap(credentials, "credential_source")["file"] != "/var/run/google/identity/token" {
				t.Fatal("ADC does not read the projected token")
			}
		})
	}
	args := []string{"template", "test", "../charts/zot-registry", "-f", "values/zot-registry.yaml", "--set", "platform.zotRegistry.storage.workloadIdentityProvider=" + provider}
	for _, settings := range []string{"platform.zotRegistry.storage.driver=s3", "platform.zotRegistry.storage.driver=gcs,platform.zotRegistry.storage.keyfile=/credentials.json"} {
		if out, err := exec.Command("helm", append(args, "--set", settings)...).CombinedOutput(); err == nil {
			t.Fatalf("invalid federation settings accepted: %s\n%s", settings, out)
		}
	}
}
