// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"encoding/json"
	"testing"
)

// TestGoogleWorkloadIdentity checks ADC files and rotating projected tokens for
// the operator and shared CSI provider; neither uses workload-token impersonation.
func TestGoogleWorkloadIdentity(t *testing.T) {
	const provider = "projects/123/locations/global/workloadIdentityPools/test/providers/kubernetes"
	for _, chart := range []string{"podplane-operator", "secrets-store-csi-provider-gcp"} {
		for _, enabled := range []bool{false, true} {
			args := []string{"--namespace", "platform-" + chart}
			providerPath := "platform.secretsStoreCSIProviderGCP.googleWorkloadIdentity.provider"
			if chart == "podplane-operator" {
				providerPath = "podplane.operator.googleWorkloadIdentity.provider"
				args = append(args, "-f", "values/podplane-operator.yaml")
			}
			if enabled {
				args = append(args, "--set", providerPath+"="+provider)
			}
			var credentials map[string]any
			var pod map[string]any
			for _, doc := range chartDocuments(t, render(t, "../charts/"+chart, args...)) {
				if doc["kind"] == "ConfigMap" && chartMap(doc, "metadata")["name"] == "google-workload-identity" {
					if err := json.Unmarshal([]byte(chartMap(doc, "data")["credentials.json"].(string)), &credentials); err != nil {
						t.Fatal(err)
					}
				}
				if doc["kind"] == "Deployment" || doc["kind"] == "DaemonSet" {
					pod = chartMap(doc, "spec", "template", "spec")
				}
				if enabled && chart == "secrets-store-csi-provider-gcp" && (doc["kind"] == "ClusterRole" || doc["kind"] == "ClusterRoleBinding") {
					t.Fatal("shared CSI identity must not mint other SAs' tokens")
				}
			}
			var projected map[string]any
			for _, raw := range pod["volumes"].([]any) {
				v := raw.(map[string]any)
				if v["name"] == "google-identity" {
					projected = chartMap(v, "projected")
				}
			}
			if !enabled {
				if credentials != nil || projected != nil {
					t.Fatal("federation unexpectedly enabled")
				}
				continue
			}
			if credentials["type"] != "external_account" || credentials["audience"] != "//iam.googleapis.com/"+provider || credentials["token_url"] != "https://sts.googleapis.com/v1/token" || chartMap(credentials, "credential_source")["file"] != "/var/run/google/identity/token" {
				t.Fatalf("incorrect ADC: %v", credentials)
			}
			if pod["serviceAccountName"] != "platform-"+chart || projected == nil {
				t.Fatalf("incorrect identity for %s: %v", chart, pod)
			}
			sources := projected["sources"].([]any)
			token := chartMap(sources[0].(map[string]any), "serviceAccountToken")
			if token["audience"] != "https://iam.googleapis.com/"+provider || token["expirationSeconds"] != 3600 || token["path"] != "token" || chartMap(sources[1].(map[string]any), "configMap")["name"] != "google-workload-identity" {
				t.Fatal("incorrect token projection", sources)
			}
			container := pod["containers"].([]any)[0].(map[string]any)
			env, mount := false, false
			for _, raw := range container["env"].([]any) {
				e := raw.(map[string]any)
				env = env || e["name"] == "GOOGLE_APPLICATION_CREDENTIALS" && e["value"] == "/var/run/google/identity/credentials.json"
			}
			for _, raw := range container["volumeMounts"].([]any) {
				m := raw.(map[string]any)
				mount = mount || m["name"] == "google-identity" && m["mountPath"] == "/var/run/google/identity" && m["readOnly"] == true && m["subPath"] == nil
			}
			if !env || !mount {
				t.Fatal("ADC env or rotating volume mount missing")
			}
		}
	}
}

// TestGoogleMetadataPolicy checks metadata API denial without default-denying
// other egress, host traffic, or Google's DNS service on the same address.
func TestGoogleMetadataPolicy(t *testing.T) {
	for _, enabled := range []string{"false", "true"} {
		var spec map[string]any
		for _, doc := range chartDocuments(t, render(t, "../charts/cilium", "--set", "platform.cilium.blockGoogleMetadata="+enabled)) {
			if doc["kind"] == "CiliumClusterwideNetworkPolicy" {
				spec = chartMap(doc, "spec")
			}
		}
		if enabled == "false" {
			if spec != nil {
				t.Fatal("Google-only policy enabled")
			}
			continue
		}
		if spec == nil || spec["nodeSelector"] != nil || spec["endpointSelector"] == nil || chartMap(spec, "enableDefaultDeny")["egress"] != false {
			t.Fatal("incorrect metadata policy selector/default", spec)
		}
		rule := spec["egressDeny"].([]any)[0].(map[string]any)
		cidrs := rule["toCIDR"].([]any)
		if len(cidrs) != 2 || cidrs[0] != "169.254.169.254/32" || cidrs[1] != "fd20:ce::254/128" {
			t.Fatal("missing metadata address", cidrs)
		}
		ports := rule["toPorts"].([]any)[0].(map[string]any)["ports"].([]any)
		if len(ports) != 2 || ports[0].(map[string]any)["port"] != "80" || ports[1].(map[string]any)["port"] != "443" {
			t.Fatal("metadata denial must preserve DNS", ports)
		}
	}
}
