// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestRuntimeClassPolicy evaluates which RuntimeClasses new Pods may use.
func TestRuntimeClassPolicy(t *testing.T) {
	policy := renderValidatingPolicy(t, "../charts/platform-runtime", "platform-runtime-class-vap")
	tenant := map[string]any{"username": "system:serviceaccount:kube-system:replicaset-controller", "groups": []string{"system:serviceaccounts"}}
	node := map[string]any{"username": "system:node:i-123", "groups": []string{"system:nodes", "system:authenticated"}}
	admin := map[string]any{"username": "alice", "groups": []string{"podplane:admins"}}
	mirror := map[string]any{"kubernetes.io/config.mirror": "abc"}
	for _, test := range []struct {
		name, namespace string
		runtime         any // runtimeClassName; nil omits it
		permission      any // runtime.podplane.dev/runc label value; nil omits it
		annotations     map[string]any
		user            map[string]any
		want            bool
	}{
		{"gvisor", "team-a", "gvisor", nil, nil, tenant, true},
		{"runc without permission", "team-a", "runc", nil, nil, tenant, false},
		{"runc with permission", "team-a", "runc", "allowed", nil, tenant, true},
		{"gvisor with permission", "team-a", "gvisor", "allowed", nil, tenant, true},
		{"permission value true", "team-a", "runc", "true", nil, tenant, false},
		{"permission value Allowed", "team-a", "runc", "Allowed", nil, tenant, false},
		{"empty permission value", "team-a", "runc", "", nil, tenant, false},
		{"admins are not exempt", "team-a", "runc", nil, nil, admin, false},
		{"missing after defaulting", "team-a", nil, "allowed", nil, tenant, false},
		{"explicit empty", "team-a", "", "allowed", nil, tenant, false},
		{"alternate class backed by runc", "team-a", "native", "allowed", nil, tenant, false},
		{"kubelet static Pod mirror", "kube-system", nil, nil, mirror, node, true},
		{"forged mirror from admin", "kube-system", nil, nil, mirror, admin, false},
		{"node mirror outside kube-system", "team-a", nil, nil, mirror, node, false},
		{"node Pod without mirror annotation", "kube-system", nil, nil, nil, node, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := map[string]any{}
			if test.runtime != nil {
				spec["runtimeClassName"] = test.runtime
			}
			metadata := map[string]any{}
			if test.annotations != nil {
				metadata["annotations"] = test.annotations
			}
			labels := map[string]any{}
			if test.permission != nil {
				labels["runtime.podplane.dev/runc"] = test.permission
			}
			allowed := policy.admits(t, map[string]any{
				"object":          map[string]any{"metadata": metadata, "spec": spec},
				"namespaceObject": map[string]any{"metadata": map[string]any{"name": test.namespace, "labels": labels}},
				"request":         map[string]any{"namespace": test.namespace, "userInfo": test.user},
			})
			if allowed != test.want {
				t.Fatalf("allowed = %v, want %v", allowed, test.want)
			}
		})
	}
}

// TestRuncPermissionPolicy evaluates who may grant, change, or revoke runc
// permission on a namespace.
func TestRuncPermissionPolicy(t *testing.T) {
	policy := renderValidatingPolicy(t, "../charts/platform-runtime", "platform-runc-permission-vap")
	namespace := func(name string, labels map[string]any) map[string]any {
		return map[string]any{"metadata": map[string]any{"name": name, "labels": labels}}
	}
	granted := map[string]any{"runtime.podplane.dev/runc": "allowed"}
	unrelated := map[string]any{"owner": "team-a"}
	flux := "system:serviceaccount:platform-fluxcd:helm-controller"
	for _, test := range []struct {
		name, operation, namespace string
		old, new                   map[string]any
		username, group            string
		want                       bool
	}{
		{"manager grants on update", "UPDATE", "team-a", unrelated, granted, "bob", "podplane:managers", false},
		{"manager grants on create", "CREATE", "team-a", nil, granted, "bob", "podplane:managers", false},
		{"manager revokes", "UPDATE", "team-a", granted, unrelated, "bob", "podplane:managers", false},
		{"manager changes value", "UPDATE", "team-a", granted, map[string]any{"runtime.podplane.dev/runc": "true"}, "bob", "podplane:managers", false},
		{"manager edits unrelated label", "UPDATE", "team-a", granted, map[string]any{"runtime.podplane.dev/runc": "allowed", "owner": "team-a"}, "bob", "podplane:managers", true},
		{"manager creates namespace without permission", "CREATE", "team-a", nil, unrelated, "bob", "podplane:managers", true},
		{"admin grants", "UPDATE", "team-a", unrelated, granted, "alice", "podplane:admins", true},
		{"admin revokes", "UPDATE", "team-a", granted, unrelated, "alice", "podplane:admins", true},
		{"system:masters grants", "CREATE", "team-a", nil, granted, "kubernetes-admin", "system:masters", true},
		{"flux grants platform namespace", "CREATE", "platform-cilium", nil, granted, flux, "system:serviceaccounts", true},
		{"flux cannot grant tenant namespace", "UPDATE", "team-a", unrelated, granted, flux, "system:serviceaccounts", false},
		{"other helm-controller cannot grant", "CREATE", "platform-cilium", nil, granted, "system:serviceaccount:team-a:helm-controller", "system:serviceaccounts", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var old any
			if test.operation == "UPDATE" {
				old = namespace(test.namespace, test.old)
			}
			allowed := policy.admits(t, map[string]any{
				"object":    namespace(test.namespace, test.new),
				"oldObject": old,
				"request": map[string]any{
					"operation": test.operation,
					"userInfo":  map[string]any{"username": test.username, "groups": []string{test.group}},
				},
			})
			if allowed != test.want {
				t.Fatalf("allowed = %v, want %v", allowed, test.want)
			}
		})
	}
}

// TestPlatformRuncExceptions checks that exactly the platform namespaces whose
// Pods default to runc have runc permission, so neither list can drift into
// granting permission without need or defaulting Pods that will be rejected.
func TestPlatformRuncExceptions(t *testing.T) {
	var runtime struct {
		Platform struct {
			Runtime struct {
				RuncDefaultNamespaces []string `yaml:"runcDefaultNamespaces"`
			} `yaml:"runtime"`
		} `yaml:"platform"`
	}
	var components struct {
		Platform struct {
			Components struct {
				Apps map[string]struct {
					Namespace   string `yaml:"namespace"`
					RuncAllowed bool   `yaml:"runcAllowed"`
				} `yaml:"apps"`
			} `yaml:"components"`
		} `yaml:"platform"`
	}
	for path, values := range map[string]any{
		"../charts/platform-runtime/values.yaml":    &runtime,
		"../charts/platform-components/values.yaml": &components,
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(data, values); err != nil {
			t.Fatal(err)
		}
	}
	var permitted []string
	for _, app := range components.Platform.Components.Apps {
		if app.RuncAllowed {
			permitted = append(permitted, app.Namespace)
		}
	}
	defaults := slices.Sorted(slices.Values(runtime.Platform.Runtime.RuncDefaultNamespaces))
	slices.Sort(permitted)
	if !slices.Equal(permitted, defaults) {
		t.Errorf("runcAllowed namespaces %v differ from runcDefaultNamespaces %v", permitted, defaults)
	}

	// The namespace label is the permission admission checks.
	docs := chartDocuments(t, render(t, "../charts/platform-components", "--namespace", "platform-components",
		"-f", "values/platform-components.yaml"))
	for _, doc := range docs {
		switch doc["kind"] {
		case "Namespace":
			name := chartMap(doc, "metadata")["name"].(string)
			labelled := chartMap(doc, "metadata", "labels")["runtime.podplane.dev/runc"] == "allowed"
			if labelled != slices.Contains(defaults, name) {
				t.Errorf("%s: runc permission label = %v", name, labelled)
			}
		case "HelmRelease":
			if _, ok := chartMap(doc, "spec")["postRenderers"]; ok {
				t.Errorf("HelmRelease %v must not post-render charts", chartMap(doc, "metadata")["name"])
			}
		}
	}

	out, err := exec.Command("helm", "template", "test", "../charts/platform-runtime", "--kube-version", "1.37.0",
		"--set", "platform.runtime.runcDefaultNamespaces={team-a}").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "only platform-* namespaces") {
		t.Errorf("a tenant namespace in runcDefaultNamespaces must fail to render: %v\n%s", err, out)
	}
}

// TestEnvoyProxySharesSDSSocketUnderGVisor checks that the Envoy proxy and its
// SDS sidecar share their socket volume when running in gVisor, which otherwise
// gives each container a separate view of an emptyDir.
func TestEnvoyProxySharesSDSSocketUnderGVisor(t *testing.T) {
	for _, doc := range envoyIngressDocuments(t) {
		if doc["kind"] != "EnvoyProxy" {
			continue
		}
		template := chartMap(doc, "spec", "provider", "kubernetes", "envoyDaemonSet", "patch", "value", "spec", "template")
		annotations := chartMap(template, "metadata", "annotations")
		for key, want := range map[string]string{"share": "pod", "type": "tmpfs"} {
			if got := annotations["dev.gvisor.spec.mount.podplane-sds."+key]; got != want {
				t.Errorf("podplane-sds gVisor mount %s = %v, want %s", key, got, want)
			}
		}
		for _, raw := range chartMap(template, "spec")["containers"].([]any) {
			for _, mount := range raw.(map[string]any)["volumeMounts"].([]any) {
				mount := mount.(map[string]any)
				// gVisor cannot create a shared mount at a path ending in "/".
				if mount["name"] == "podplane-sds" && strings.HasSuffix(mount["mountPath"].(string), "/") {
					t.Errorf("podplane-sds mount path %v must not end in /", mount["mountPath"])
				}
			}
		}
		return
	}
	t.Fatal("ingress must render an EnvoyProxy")
}
