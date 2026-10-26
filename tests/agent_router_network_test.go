// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// TestAgentRouterNetworkIsolation evaluates the union of rendered ingress
// policies against actual controller labels, not just policy names/text.
func TestAgentRouterNetworkIsolation(t *testing.T) {
	for _, namespace := range []string{"platform-agent-router", "alternate-router"} {
		for _, nameOverride := range []string{"", "custom-router"} {
			t.Run(namespace+"/"+nameOverride, func(t *testing.T) {
				args := []string{"--namespace", namespace}
				if nameOverride != "" {
					args = append(args, "--set", "ai-gateway-helm.nameOverride="+nameOverride)
				}
				var policies []networkingv1.NetworkPolicy
				var controller labels.Set
				for _, doc := range chartDocuments(t, render(t, "../charts/agent-router", args...)) {
					if doc["kind"] == "Deployment" && chartMap(doc, "metadata")["name"] == "agent-router" {
						controller = labels.Set{}
						for key, value := range chartMap(doc, "spec", "template", "metadata", "labels") {
							controller[key] = value.(string)
						}
					}
					if doc["kind"] != "NetworkPolicy" {
						continue
					}
					data, err := json.Marshal(doc)
					if err != nil {
						t.Fatal(err)
					}
					var policy networkingv1.NetworkPolicy
					if err := json.Unmarshal(data, &policy); err != nil {
						t.Fatal(err)
					}
					if policy.Namespace != namespace || !reflect.DeepEqual(policy.Spec.PolicyTypes, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}) || len(policy.Spec.Egress) != 0 {
						t.Fatalf("policy must isolate ingress only in the release namespace: %+v", policy)
					}
					for _, rule := range policy.Spec.Ingress {
						if len(rule.From) != 0 {
							t.Fatal("operational/TLS ports must be available from all sources")
						}
					}
					policies = append(policies, policy)
				}
				if controller == nil || len(policies) != 2 {
					t.Fatalf("missing controller or policies: labels=%v, policies=%d", controller, len(policies))
				}
				proxy := labels.Set{
					"app.kubernetes.io/managed-by":                   "envoy-gateway",
					"gateway.envoyproxy.io/owning-gateway-name":      "platform-agent-router-gateway",
					"gateway.envoyproxy.io/owning-gateway-namespace": namespace,
				}
				for _, target := range []struct {
					name   string
					labels labels.Set
					ports  []int
				}{
					{"controller", controller, []int{1163, 18003, 9443, 8080}},
					{"proxy", proxy, []int{10443, 1064, 19001, 19003}},
				} {
					var selected []networkingv1.NetworkPolicy
					for _, policy := range policies {
						selector, err := metav1.LabelSelectorAsSelector(&policy.Spec.PodSelector)
						if err != nil {
							t.Fatal(err)
						}
						if selector.Matches(target.labels) {
							selected = append(selected, policy)
						}
					}
					if len(selected) != 1 {
						t.Fatalf("%s selected by %d policies, want exactly one", target.name, len(selected))
					}
					for port := 1; port <= 65535; port++ {
						for _, protocol := range []corev1.Protocol{corev1.ProtocolTCP, corev1.ProtocolUDP, corev1.ProtocolSCTP} {
							want := protocol == corev1.ProtocolTCP && slices.Contains(target.ports, port)
							if got := agentRouterIngressAllows(t, selected, protocol, port); got != want {
								t.Fatalf("%s ingress %s/%d allowed=%v, want %v", target.name, protocol, port, got, want)
							}
						}
					}
				}
				// Neither an ingress proxy nor a same-named gateway in another
				// namespace should be selected by this fleet's policy.
				for _, key := range []string{"gateway.envoyproxy.io/owning-gateway-name", "gateway.envoyproxy.io/owning-gateway-namespace"} {
					other := labels.Set{}
					for k, v := range proxy {
						other[k] = v
					}
					other[key] = "other"
					for _, policy := range policies {
						selector, _ := metav1.LabelSelectorAsSelector(&policy.Spec.PodSelector)
						if selector.Matches(other) {
							t.Fatalf("policy %s selects unrelated proxy %v", policy.Name, other)
						}
					}
				}
			})
		}
	}
}

// The caller verifies source-unrestricted rules. Here empty policy/rule/port
// lists and additive allows follow NetworkPolicy semantics, including ranges.
func agentRouterIngressAllows(t *testing.T, policies []networkingv1.NetworkPolicy, protocol corev1.Protocol, port int) bool {
	t.Helper()
	if len(policies) == 0 {
		return true
	}
	for _, policy := range policies {
		for _, rule := range policy.Spec.Ingress {
			if len(rule.Ports) == 0 {
				return true
			}
			for _, p := range rule.Ports {
				proto := corev1.ProtocolTCP
				if p.Protocol != nil {
					proto = *p.Protocol
				}
				if proto != protocol {
					continue
				}
				if p.Port == nil {
					return true
				}
				if p.Port.Type != intstr.Int {
					t.Fatal("expected numeric policy ports")
				}
				end := p.Port.IntVal
				if p.EndPort != nil {
					end = *p.EndPort
				}
				if int(p.Port.IntVal) <= port && port <= int(end) {
					return true
				}
			}
		}
	}
	return false
}

// JSONPatch supplies the admission expression's constructor to cel-go without
// importing the API server. These patches only replace string argument lists.
type JSONPatch struct {
	Op    string   `cel:"op"`
	Path  string   `cel:"path"`
	Value []string `cel:"value"`
}

func TestAgentRouterMCPLoopbackMutation(t *testing.T) {
	for _, namespace := range []string{"platform-agent-router", "alternate-router"} {
		t.Run(namespace, func(t *testing.T) {
			const name = "platform-agent-router-mcp-loopback-map"
			var spec map[string]any
			bound := false
			for _, doc := range chartDocuments(t, render(t, "../charts/agent-router", "--namespace", namespace)) {
				if doc["kind"] == "MutatingAdmissionPolicy" && chartMap(doc, "metadata")["name"] == name {
					spec = chartMap(doc, "spec")
				}
				if doc["kind"] == "MutatingAdmissionPolicyBinding" && chartMap(doc, "spec")["policyName"] == name {
					bound = true
				}
			}
			if spec == nil || !bound || spec["failurePolicy"] != "Fail" || spec["reinvocationPolicy"] != "IfNeeded" || spec["matchConditions"] != nil {
				t.Fatal("loopback mutation must fail closed, be bound, and match before webhook injection")
			}
			constraints := chartMap(spec, "matchConstraints")
			wantLabels := map[string]any{
				"app.kubernetes.io/managed-by":                   "envoy-gateway",
				"gateway.envoyproxy.io/owning-gateway-name":      "platform-agent-router-gateway",
				"gateway.envoyproxy.io/owning-gateway-namespace": namespace,
			}
			if !reflect.DeepEqual(constraints["objectSelector"], map[string]any{"matchLabels": wantLabels}) {
				t.Fatalf("mutation must select only this gateway: %v", constraints)
			}
			wantRules := []any{map[string]any{"apiGroups": []any{""}, "apiVersions": []any{"v1"}, "operations": []any{"CREATE"}, "resources": []any{"pods"}}}
			if !reflect.DeepEqual(constraints["resourceRules"], wantRules) {
				t.Fatal("mutation must apply only to Pod creation")
			}
			env, err := cel.NewEnv(cel.OptionalTypes(), ext.TwoVarComprehensions(), cel.Variable("object", cel.DynType),
				cel.Container("tests"), ext.NativeTypes(reflect.TypeFor[JSONPatch](), ext.ParseStructTags(true)))
			if err != nil {
				t.Fatal(err)
			}
			var programs []cel.Program
			for _, raw := range spec["mutations"].([]any) {
				mutation := raw.(map[string]any)
				if mutation["patchType"] != "JSONPatch" {
					t.Fatal("expected JSONPatch")
				}
				ast, issues := env.Compile(chartMap(mutation, "jsonPatch")["expression"].(string))
				if issues.Err() != nil {
					t.Fatal(issues.Err())
				}
				program, err := env.Program(ast)
				if err != nil {
					t.Fatal(err)
				}
				programs = append(programs, program)
			}
			if len(programs) != 2 {
				t.Fatal("both regular and native sidecars need mutation")
			}
			apply := func(pod map[string]any) int {
				count := 0
				for _, program := range programs {
					out, _, err := program.Eval(map[string]any{"object": pod})
					if err != nil {
						t.Fatal(err)
					}
					native, err := out.ConvertToNative(reflect.TypeFor[[]JSONPatch]())
					if err != nil {
						t.Fatal(err)
					}
					for _, patch := range native.([]JSONPatch) {
						parts := strings.Split(patch.Path, "/")
						if patch.Op != "replace" || len(parts) != 5 || parts[1] != "spec" || parts[4] != "args" {
							t.Fatalf("unexpected patch: %+v", patch)
						}
						index, err := strconv.Atoi(parts[3])
						if err != nil {
							t.Fatal(err)
						}
						// Round-trip the dynamic CEL list to the same JSON value
						// representation as the original admission object.
						data, err := json.Marshal(patch.Value)
						if err != nil {
							t.Fatal(err)
						}
						var args any
						if err := json.Unmarshal(data, &args); err != nil {
							t.Fatal(err)
						}
						chartMap(pod, "spec")[parts[2]].([]any)[index].(map[string]any)["args"] = args
						count++
					}
				}
				return count
			}
			for _, field := range []string{"containers", "initContainers"} {
				for _, test := range []struct {
					name       string
					args, want []any
				}{
					{"injected", []any{"-logLevel", "info", "-mcpAddr", ":9856", "-adminPort", "1064"}, []any{"-logLevel", "info", "-mcpAddr", "127.0.0.1:9856", "-adminPort", "1064"}},
					{"unrelated identical value", []any{":9856", "-other", ":9856", "-mcpAddr", ":9856"}, []any{":9856", "-other", ":9856", "-mcpAddr", "127.0.0.1:9856"}},
					{"already loopback", []any{"-mcpAddr", "127.0.0.1:9856"}, []any{"-mcpAddr", "127.0.0.1:9856"}},
					{"custom address", []any{"-mcpAddr", ":9999"}, []any{"-mcpAddr", ":9999"}},
					{"unrelated flag", []any{"-other", ":9856"}, []any{"-other", ":9856"}},
					{"missing value", []any{"-mcpAddr"}, []any{"-mcpAddr"}},
					{"no args", nil, nil},
				} {
					t.Run(field+"/"+test.name, func(t *testing.T) {
						pod := map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "envoy"}}}}
						if apply(pod) != 0 {
							t.Fatal("first admission pass must not patch before injection")
						}
						sidecar := map[string]any{"name": "ai-gateway-extproc", "image": "unchanged", "restartPolicy": "Always"}
						if test.args != nil {
							sidecar["args"] = test.args
						}
						other := map[string]any{"name": "other", "args": []any{"-mcpAddr", ":9856"}}
						chartMap(pod, "spec")[field] = []any{other, sidecar}
						changed := apply(pod)
						wantChanges := 0
						if !reflect.DeepEqual(test.args, test.want) {
							wantChanges = 1
						}
						if changed != wantChanges || (test.want != nil && !reflect.DeepEqual(sidecar["args"], test.want)) {
							t.Fatalf("changed=%d, args=%v, want %v", changed, sidecar["args"], test.want)
						}
						if !reflect.DeepEqual(other["args"], []any{"-mcpAddr", ":9856"}) || sidecar["image"] != "unchanged" || sidecar["restartPolicy"] != "Always" {
							t.Fatal("unrelated container/fields changed")
						}
						if apply(pod) != 0 {
							t.Fatal("mutation must be idempotent on reinvocation")
						}
					})
				}
			}
		})
	}
}
