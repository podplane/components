// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// TestSecretProviderClassWriters rejects raw SPC bypasses, even with forged ownership.
func TestSecretProviderClassWriters(t *testing.T) {
	policy := renderValidatingPolicy(t, "../charts/platform-rbac", "platform-spc-writer-vap", "--namespace", "platform-cluster")
	for _, tc := range []struct {
		user, group string
		want        bool
	}{
		{"alice", "podplane:admins", true},
		{"root", "system:masters", true},
		{"system:serviceaccount:platform-podplane-operator:platform-podplane-operator", "system:serviceaccounts", true},
		{"system:serviceaccount:platform-fluxcd:helm-controller", "system:serviceaccounts", true},
		{"alice", "podplane:managers", false},
		{"alice", "podplane:editors", false},
		{"system:serviceaccount:team-a:platform-podplane-operator", "system:serviceaccounts", false},
		{"system:serviceaccount:platform-cluster:platform-podplane-operator", "system:serviceaccounts", false},
		{"system:serviceaccount:team-a:helm-controller", "system:serviceaccounts", false},
	} {
		got := policy.admits(t, map[string]any{
			"request": map[string]any{"userInfo": map[string]any{"username": tc.user, "groups": []string{tc.group}}},
			"object":  map[string]any{"metadata": map[string]any{"labels": map[string]any{"app.kubernetes.io/managed-by": "podplane"}, "ownerReferences": []any{map[string]any{"kind": "SecretProviderBinding", "name": "stolen"}}}},
		})
		if got != tc.want {
			t.Errorf("%s (%s): allowed=%v, want %v", tc.user, tc.group, got, tc.want)
		}
	}
}

// TestReserveNamespacePrefixesPolicy evaluates who may create kube-* and platform-* namespaces.
func TestReserveNamespacePrefixesPolicy(t *testing.T) {
	policy := renderValidatingPolicy(t, "../charts/platform-rbac", "platform-reserve-namespace-prefixes-vap")
	for _, test := range []struct {
		name, namespace, username, group string
		want                             bool
	}{
		{"admin creates platform namespace", "platform-foo", "alice", "podplane:admins", true},
		{"admin creates kube namespace", "kube-foo", "alice", "podplane:admins", true},
		{"system:masters creates kube namespace", "kube-foo", "system:apiserver", "system:masters", true},
		{"flux creates platform namespace", "platform-foo", "system:serviceaccount:platform-fluxcd:helm-controller", "system:serviceaccounts", true},
		{"manager cannot create platform namespace", "platform-foo", "bob", "podplane:managers", false},
		{"manager cannot create kube namespace", "kube-foo", "bob", "podplane:managers", false},
		{"other helm-controller cannot", "platform-foo", "system:serviceaccount:team-a:helm-controller", "system:serviceaccounts", false},
		{"unprefixed lookalike allowed", "platformer", "bob", "podplane:managers", true},
		{"tenant namespace allowed", "team-a", "bob", "podplane:managers", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			allowed := policy.admits(t, map[string]any{
				"object":  map[string]any{"metadata": map[string]any{"name": test.namespace}},
				"request": map[string]any{"userInfo": map[string]any{"username": test.username, "groups": []string{test.group}}},
			})
			if allowed != test.want {
				t.Fatalf("allowed = %v, want %v", allowed, test.want)
			}
		})
	}
}

// TestPodplaneOperatorNamespaceGroupBindings checks the operator config and the RBAC it
// needs to grant tenant roles without holding them.
func TestPodplaneOperatorNamespaceGroupBindings(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		args := []string{"-f", "values/podplane-operator.yaml"}
		if !enabled {
			args = append(args, "--set", "podplane.operator.config.rbac.namespaceGroupBindings=false")
		}
		var config struct {
			RBAC struct {
				NamespaceGroupBindings bool `json:"namespace_group_bindings"`
			} `json:"rbac"`
		}
		var bind, roleBindings []string
		for _, doc := range chartDocuments(t, render(t, "../charts/podplane-operator", args...)) {
			switch doc["kind"] {
			case "ConfigMap":
				if raw, ok := chartMap(doc, "data")["config.json"].(string); ok {
					if err := json.Unmarshal([]byte(raw), &config); err != nil {
						t.Fatal(err)
					}
				}
			case "ClusterRole":
				if chartMap(doc, "metadata")["name"] != "platform-podplane-operator" {
					continue
				}
				for _, raw := range doc["rules"].([]any) {
					rule := raw.(map[string]any)
					resources := rule["resources"].([]any)
					if slices.Contains(resources, any("clusterroles")) && slices.Contains(rule["verbs"].([]any), any("bind")) {
						for _, name := range rule["resourceNames"].([]any) {
							bind = append(bind, name.(string))
						}
					}
					if slices.Contains(resources, any("rolebindings")) {
						for _, verb := range rule["verbs"].([]any) {
							roleBindings = append(roleBindings, verb.(string))
						}
					}
				}
			}
		}
		if config.RBAC.NamespaceGroupBindings != enabled {
			t.Fatalf("enabled=%v: config namespace_group_bindings = %v", enabled, config.RBAC.NamespaceGroupBindings)
		}
		if !enabled {
			if len(bind) != 0 || len(roleBindings) != 0 {
				t.Fatalf("disabled namespace group bindings must not grant bind %v or rolebindings %v", bind, roleBindings)
			}
			continue
		}
		if !slices.Equal(bind, []string{"admin", "edit", "view"}) {
			t.Fatalf("bind resourceNames = %v, want exactly admin, edit, view", bind)
		}
		for _, verb := range []string{"list", "watch", "create", "update", "delete"} {
			if !slices.Contains(roleBindings, verb) {
				t.Fatalf("rolebindings verbs %v missing %s", roleBindings, verb)
			}
		}
	}
}

// TestFluxDoesNotAggregateIntoTenantRoles checks that the built-in admin, edit,
// and view roles grant no Flux access, since helm-controller runs as cluster-admin.
func TestFluxDoesNotAggregateIntoTenantRoles(t *testing.T) {
	for _, doc := range chartDocuments(t, render(t, "../charts/fluxcd", "--namespace", "platform-fluxcd")) {
		if doc["kind"] != "ClusterRole" {
			continue
		}
		for label := range chartMap(doc, "metadata", "labels") {
			if strings.HasPrefix(label, "rbac.authorization.k8s.io/aggregate-to-") {
				t.Fatalf("ClusterRole %v aggregates into %s", chartMap(doc, "metadata")["name"], label)
			}
		}
	}
}

// TestPodSecurityDefaultsNamespaces checks which namespaces receive secure Pod
// defaults; privileged components would break if their namespaces were mutated.
func TestPodSecurityDefaultsNamespaces(t *testing.T) {
	var selector *metav1.LabelSelector
	for _, doc := range chartDocuments(t, render(t, "../charts/platform-rbac")) {
		if doc["kind"] == "MutatingAdmissionPolicyBinding" && chartMap(doc, "spec")["policyName"] == "platform-pod-security-defaults-map" {
			raw, _ := json.Marshal(chartMap(doc, "spec", "matchResources", "namespaceSelector"))
			if err := json.Unmarshal(raw, &selector); err != nil {
				t.Fatal(err)
			}
		}
	}
	if selector == nil {
		t.Fatal("pod security defaults policy must be bound with a namespace selector")
	}
	matcher, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		namespace, enforce string
		want               bool
	}{
		{"team-a", "", true},
		{"team-a", "restricted", true},
		{"platform-fluxcd", "restricted", true},
		{"team-a", "baseline", true},
		{"platform-cilium", "privileged", false},
		{"kube-system", "", false},
	} {
		set := labels.Set{"kubernetes.io/metadata.name": test.namespace}
		if test.enforce != "" {
			set["pod-security.kubernetes.io/enforce"] = test.enforce
		}
		if got := matcher.Matches(set); got != test.want {
			t.Errorf("%s (enforce=%q): mutated=%v, want %v", test.namespace, test.enforce, got, test.want)
		}
	}
}
