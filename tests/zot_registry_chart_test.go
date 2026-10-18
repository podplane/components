// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestZotGoogleSecret verifies GCS keyfile configuration and CSI secret references.
func TestZotGoogleSecret(t *testing.T) {
	const keyfile = "/var/run/google/credentials.json"
	for _, version := range []string{"", "latest", "7"} {
		t.Run("version="+version, func(t *testing.T) {
			resource := "projects/123456/secrets/registry-writer/versions/" + version
			args := []string{"-f", "values/zot-registry.yaml", "--set", "platform.zotRegistry.storage.driver=gcs", "--set", "zot.serviceAccount.create=true,zot.serviceAccount.name=zot-registry"}
			if version != "" {
				args = append(args, "--set", "platform.zotRegistry.storage.googleSecretVersion="+resource, "--set", "platform.zotRegistry.storage.keyfile="+keyfile)
			}
			var data, spc map[string]any
			for _, doc := range chartDocuments(t, render(t, "../charts/zot-registry", args...)) {
				if doc["kind"] == "ConfigMap" && chartMap(doc, "metadata")["name"] == "zot-registry-config" {
					data = chartMap(doc, "data")
				}
				if doc["kind"] == "SecretProviderClass" {
					spc = doc
				}
			}
			if data == nil {
				t.Fatal("registry configuration missing")
			}
			if data["credentials.json"] != nil {
				t.Fatal("credentials must not be emitted in the ConfigMap")
			}
			var config map[string]any
			if err := json.Unmarshal([]byte(data["config.json"].(string)), &config); err != nil {
				t.Fatal(err)
			}
			driver := chartMap(config, "storage", "storageDriver")
			if driver["name"] != "gcs" || driver["rootdirectory"] != "//" || driver["credentials"] != nil {
				t.Fatalf("unexpected GCS configuration: %v", driver)
			}
			if version == "" {
				if spc != nil || driver["keyfile"] != nil {
					t.Fatal("ambient ADC unexpectedly overridden")
				}
				return
			}
			if driver["keyfile"] != keyfile || chartMap(spc, "metadata")["name"] != "zot-registry" || chartMap(spc, "spec")["provider"] != "gcp" {
				t.Fatalf("incorrect keyfile or SecretProviderClass: %v %v", driver, spc)
			}
			parameters := chartMap(spc, "spec", "parameters")
			var secrets []map[string]string
			if err := yaml.Unmarshal([]byte(parameters["secrets"].(string)), &secrets); err != nil {
				t.Fatal(err)
			}
			if parameters["auth"] != "provider-adc" || len(secrets) != 1 || secrets[0]["resourceName"] != resource || secrets[0]["path"] != "credentials.json" {
				t.Fatalf("incorrect CSI parameters: %v", parameters)
			}
		})
	}
	args := []string{"template", "test", "../charts/zot-registry", "-f", "values/zot-registry.yaml", "--set", "platform.zotRegistry.storage.googleSecretVersion=projects/123/secrets/writer/versions/latest"}
	for _, settings := range []string{"platform.zotRegistry.storage.driver=s3,platform.zotRegistry.storage.keyfile=" + keyfile, "platform.zotRegistry.storage.driver=gcs"} {
		out, err := exec.Command("helm", append(args, "--set", settings)...).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "googleSecretVersion requires gcs storage and a configured keyfile") {
			t.Fatalf("expected invalid Google secret settings rejection: %s\n%s", settings, out)
		}
	}
}
