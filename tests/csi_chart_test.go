// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDefaultStorageClasses locks the portable PVC contract for each provider.
func TestDefaultStorageClasses(t *testing.T) {
	for _, tc := range []struct {
		chart, name, driver, reclaim string
		parameters                   map[string]any
	}{
		{"csi-aws-ebs", "platform-aws-ebs-default", "ebs.csi.aws.com", "Retain", map[string]any{"encrypted": "true"}},
		{"csi-gce-pd", "platform-gce-pd-default", "pd.csi.storage.gke.io", "Retain", map[string]any{"type": "pd-balanced", "csi.storage.k8s.io/fstype": "ext4"}},
		{"csi-openebs-lvm", "platform-openebs-lvm-default", "local.csi.openebs.io", "Delete", map[string]any{"storage": "lvm", "volgroup": "podplane", "fsType": "ext4"}},
	} {
		t.Run(tc.chart, func(t *testing.T) {
			classes := 0
			for _, doc := range chartDocuments(t, render(t, "../charts/"+tc.chart)) {
				if doc["kind"] != "StorageClass" {
					continue
				}
				classes++
				if chartMap(doc, "metadata")["name"] != tc.name || chartMap(doc, "metadata", "annotations")["storageclass.kubernetes.io/is-default-class"] != "true" || doc["provisioner"] != tc.driver || doc["reclaimPolicy"] != tc.reclaim || doc["volumeBindingMode"] != "WaitForFirstConsumer" || doc["allowVolumeExpansion"] != true || !reflect.DeepEqual(doc["parameters"], tc.parameters) {
					t.Fatalf("incorrect default class: %v", doc)
				}
				if tc.chart == "csi-openebs-lvm" && !reflect.DeepEqual(doc["mountOptions"], []any{"discard"}) {
					t.Fatal("local volumes must discard freed blocks")
				}
			}
			if classes != 1 {
				t.Fatalf("got %d StorageClasses, want exactly one", classes)
			}
		})
	}
}

// TestCSIMirrorAndNamespaces exercises registry values through Flux into real
// workload renders, including every upstream sidecar, not just the main image.
func TestCSIMirrorAndNamespaces(t *testing.T) {
	for _, name := range []string{"csi-aws-ebs", "csi-gce-pd", "csi-openebs-lvm"} {
		t.Run(name, func(t *testing.T) {
			args := []string{"--set", "platform.components.apps." + name + ".enabled=true", "--set", "platform.components.imageMirror.enabled=true", "--set", "platform.components.imageMirror.hostname=registry.example"}
			var values map[string]any
			namespace := false
			for _, doc := range chartDocuments(t, render(t, "../charts/platform-components", args...)) {
				if doc["kind"] == "Namespace" && chartMap(doc, "metadata")["name"] == "platform-"+name {
					labels := chartMap(doc, "metadata", "labels")
					namespace = labels["pod-security.kubernetes.io/enforce"] == "privileged" && labels["runtime.podplane.dev/runc"] == "allowed"
				}
				if doc["kind"] == "HelmRelease" && chartMap(doc, "metadata")["name"] == name {
					values = chartMap(doc, "spec", "values")
				}
			}
			if !namespace || values == nil {
				t.Fatal("missing privileged runc namespace or mirrored release")
			}
			data, err := yaml.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "values.yaml")
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			images := 0
			for _, doc := range chartDocuments(t, render(t, "../charts/"+name, "-f", file)) {
				// Flux does not run upstream Helm test hooks.
				hook, _ := chartMap(doc, "metadata", "annotations")["helm.sh/hook"].(string)
				if strings.Contains(hook, "test") {
					continue
				}
				pod := chartMap(podTemplate(doc), "spec")
				for _, key := range []string{"containers", "initContainers"} {
					containers, _ := pod[key].([]any)
					for _, raw := range containers {
						image := raw.(map[string]any)["image"].(string)
						if !strings.HasPrefix(image, "registry.example/mirror/") {
							t.Errorf("unmirrored image %s", image)
						}
						images++
					}
				}
			}
			if images == 0 {
				t.Fatal("no images tested")
			}
			if !strings.Contains(render(t, "../charts/platform-runtime"), "platform-"+name) {
				t.Fatal("namespace missing from runc defaults")
			}
		})
	}
}

// TestGCEPDIsolation verifies only the driver controller receives federation,
// never taint-controller or the host-networked node plugin.
func TestGCEPDIsolation(t *testing.T) {
	const provider = "projects/123/locations/global/workloadIdentityPools/test/providers/kubernetes"
	rendered := render(t, "../charts/csi-gce-pd", "--namespace", "platform-csi-gce-pd", "--set", "platform.csiGcePd.googleWorkloadIdentity.provider="+provider, "--set", "platform.csiGcePd.projectID=test-project", "--set", "platform.csiGcePd.zone=us-central1-a")
	for _, forbidden := range []string{"cloud-sa", "snapshotter", "-win", "podsecuritypolicies", "token-url"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("forbidden upstream feature %q", forbidden)
		}
	}
	workloads := 0
	for _, doc := range chartDocuments(t, rendered) {
		if strings.HasSuffix(chartMap(doc, "metadata")["name"].(string), "-deploy") {
			t.Fatal("obsolete deploy role retained")
		}
		if doc["kind"] == "ConfigMap" {
			data := chartMap(doc, "data")
			if config, ok := data["credentials.json"].(string); ok {
				var credentials map[string]any
				if err := json.Unmarshal([]byte(config), &credentials); err != nil {
					t.Fatal(err)
				}
				if credentials["type"] != "external_account" || credentials["audience"] != "//iam.googleapis.com/"+provider || credentials["token_url"] != "https://sts.googleapis.com/v1/token" || chartMap(credentials, "credential_source")["file"] != "/var/run/google/identity/token" || credentials["service_account_impersonation_url"] != nil {
					t.Fatal("invalid federation", credentials)
				}
			}
			if config, ok := data["cloud.conf"].(string); ok && config != "[Global]\nproject-id = test-project\nzone = us-central1-a\n" {
				t.Fatal("incorrect cloud config", config)
			}
		}
		if doc["kind"] != "Deployment" && doc["kind"] != "DaemonSet" {
			continue
		}
		workloads++
		controller := doc["kind"] == "Deployment"
		pod := chartMap(doc, "spec", "template", "spec")
		if controller {
			if pod["hostNetwork"] == true || pod["serviceAccountName"] != "platform-csi-gce-pd-controller" {
				t.Fatal("controller identity/network", pod)
			}
		} else if pod["hostNetwork"] != true || pod["affinity"] != nil || pod["serviceAccountName"] != "platform-csi-gce-pd-node" {
			t.Fatal("node placement/identity", pod)
		}
		identity := 0
		for _, raw := range pod["containers"].([]any) {
			container := raw.(map[string]any)
			wantIdentity := controller && container["name"] == "gce-pd-driver"
			envs, _ := container["env"].([]any)
			found := false
			for _, raw := range envs {
				env := raw.(map[string]any)
				if env["name"] == "GOOGLE_APPLICATION_CREDENTIALS" {
					found = env["value"] == "/var/run/google/identity/credentials.json"
				}
			}
			if found != wantIdentity {
				t.Fatal("credentials leaked or missing", container)
			}
			mounts, _ := container["volumeMounts"].([]any)
			mounted := false
			for _, raw := range mounts {
				mount := raw.(map[string]any)
				if mount["name"] == "google-identity" {
					mounted = mount["readOnly"] == true && mount["subPath"] == nil && mount["mountPath"] == "/var/run/google/identity"
				}
			}
			if mounted != wantIdentity {
				t.Fatal("rotating credential mount leaked or missing", container)
			}
			if wantIdentity {
				args := container["args"].([]any)
				if !slices.Contains(args, any("--fail-close-on-auth-error=true")) || !slices.Contains(args, any("--cloud-config=/etc/gce/cloud.conf")) {
					t.Fatal("controller must fail closed and bypass startup metadata")
				}
			}
		}
		for _, raw := range pod["volumes"].([]any) {
			volume := raw.(map[string]any)
			if volume["name"] != "google-identity" {
				continue
			}
			identity++
			sources := chartMap(volume, "projected")["sources"].([]any)
			token := chartMap(sources[0].(map[string]any), "serviceAccountToken")
			if token["audience"] != "https://iam.googleapis.com/"+provider || token["expirationSeconds"] != 3600 || token["path"] != "token" || chartMap(sources[1].(map[string]any), "configMap")["name"] != "google-workload-identity" {
				t.Fatal("incorrect projected token", sources)
			}
		}
		if controller && identity != 1 || !controller && identity != 0 {
			t.Fatal("incorrect identity volume count")
		}
	}
	if workloads != 2 {
		t.Fatal("missing Linux controller/node")
	}
}

// TestLocalStorageCRDOwnership locks the split between OpenEBS and snapshots.
func TestLocalStorageCRDOwnership(t *testing.T) {
	rendered := render(t, "../charts/csi-openebs-lvm")
	if strings.Contains(rendered, "kind: CustomResourceDefinition") || strings.Contains(rendered, "name: snapshot-controller") || strings.Contains(rendered, "VolumeSnapshotClass") {
		t.Fatal("app must not own CRDs or snapshot controller/classes")
	}
	if !strings.Contains(rendered, "name: csi-snapshotter") {
		t.Fatal("upstream snapshotter assumption changed")
	}
	docs := chartDocuments(t, render(t, "../charts/csi-openebs-lvm-crds"))
	if len(docs) != 3 {
		t.Fatalf("got %d CRDs, want three", len(docs))
	}
	want := map[string]bool{"LVMNode": true, "LVMVolume": true, "LVMSnapshot": true}
	for _, doc := range docs {
		spec := chartMap(doc, "spec")
		kind, _ := chartMap(spec, "names")["kind"].(string)
		if doc["kind"] != "CustomResourceDefinition" || spec["group"] != "local.openebs.io" || !want[kind] {
			t.Fatal("unexpected CRD", doc)
		}
		delete(want, kind)
		versions := spec["versions"].([]any)
		if len(versions) != 1 || versions[0].(map[string]any)["name"] != "v1alpha1" || versions[0].(map[string]any)["served"] != true {
			t.Fatal("LocalPV must serve v1alpha1", versions)
		}
	}
	for _, doc := range chartDocuments(t, render(t, "../charts/platform-components", "--set", "platform.components.apps.csi-openebs-lvm.enabled=true")) {
		if doc["kind"] != "HelmRelease" || chartMap(doc, "metadata")["name"] != "csi-openebs-lvm" {
			continue
		}
		deps := chartMap(doc, "spec")["dependsOn"].([]any)
		for _, name := range []string{"csi-openebs-lvm-crds", "snapshot-crds"} {
			found := false
			for _, dep := range deps {
				found = found || dep.(map[string]any)["name"] == name
			}
			if !found {
				t.Errorf("missing dependency %s", name)
			}
		}
	}
}
