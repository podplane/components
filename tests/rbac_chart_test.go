// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/ext"
)

// TestReserveNamespacePrefixesPolicy evaluates who may create kube-* and platform-* namespaces.
func TestReserveNamespacePrefixesPolicy(t *testing.T) {
	var policy map[string]any
	bound := false
	for _, doc := range chartDocuments(t, render(t, "../charts/platform-rbac")) {
		switch {
		case doc["kind"] == "ValidatingAdmissionPolicy" && chartMap(doc, "metadata")["name"] == "platform-reserve-namespace-prefixes-vap":
			policy = chartMap(doc, "spec")
		case doc["kind"] == "ValidatingAdmissionPolicyBinding" && chartMap(doc, "spec")["policyName"] == "platform-reserve-namespace-prefixes-vap":
			bound = slices.Contains(chartMap(doc, "spec")["validationActions"].([]any), any("Deny"))
		}
	}
	if policy == nil || !bound || policy["failurePolicy"] != "Fail" {
		t.Fatal("namespace prefix policy must exist, fail closed, and be bound with Deny")
	}
	env, err := cel.NewEnv(ext.Strings(ext.StringsVersion(2)), cel.Variable("object", cel.DynType), cel.Variable("request", cel.DynType))
	if err != nil {
		t.Fatal(err)
	}
	var programs []cel.Program
	for _, raw := range policy["validations"].([]any) {
		ast, issues := env.Compile(raw.(map[string]any)["expression"].(string))
		if issues.Err() != nil {
			t.Fatal(issues.Err())
		}
		program, err := env.Program(ast)
		if err != nil {
			t.Fatal(err)
		}
		programs = append(programs, program)
	}
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
			input := map[string]any{
				"object":  map[string]any{"metadata": map[string]any{"name": test.namespace}},
				"request": map[string]any{"userInfo": map[string]any{"username": test.username, "groups": []string{test.group}}},
			}
			allowed := true
			for _, program := range programs {
				out, _, err := program.Eval(input)
				if err != nil {
					t.Fatal(err)
				}
				allowed = allowed && out == types.True
			}
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
