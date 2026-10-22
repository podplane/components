// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/ext"
	"gopkg.in/yaml.v3"
)

// TestBootstrapInstallProfiles locks the addon membership of each public profile.
func TestBootstrapInstallProfiles(t *testing.T) {
	platform := map[string][]string{
		"crds": {"podplane-operator-crds", "secrets-store-csi-driver-crds", "envoy-gateway-crds", "csi-openebs-lvm-crds", "snapshot-crds"},
		"apps": {"podplane-operator", "secrets-store-csi-driver", "secrets-store-csi-provider-openbao", "envoy-gateway", "zot-registry", "csi-openebs-lvm"},
	}
	for _, profile := range []string{"default", "minimal", "platform", "agentic", "all"} {
		t.Run(profile, func(t *testing.T) {
			args := []string{}
			if profile != "default" {
				args = append(args, "--set", "bootstrap.install="+profile)
			}
			var components map[string]any
			for _, doc := range chartDocuments(t, render(t, "../bootstrap", args...)) {
				if doc["kind"] == "HelmRelease" {
					components = chartMap(doc, "spec", "values", "platform", "components")
				}
			}
			if components == nil {
				t.Fatal("missing platform-components HelmRelease")
			}
			for _, section := range []string{"apps", "crds"} {
				want := map[string]any{}
				if profile == "platform" || profile == "agentic" || profile == "all" {
					for _, name := range platform[section] {
						want[name] = map[string]any{"enabled": true}
					}
				}
				if profile == "agentic" || profile == "all" {
					for _, name := range []string{"agent-sandbox", "sourcegateway", "agent-router"} {
						if section == "crds" {
							name += "-crds"
						}
						want[name] = map[string]any{"enabled": true}
					}
				}
				if profile == "all" {
					extra := []string{"snapshot-crds"}
					if section == "apps" {
						extra = []string{"secrets-store-csi-provider-aws", "secrets-store-csi-provider-gcp", "secrets-store-csi-provider-vault", "snapshot", "metrics-server", "node-problem-detector", "cluster-autoscaler"}
					}
					for _, name := range extra {
						want[name] = map[string]any{"enabled": true}
					}
				}
				got, _ := components[section].(map[string]any)
				if len(want) == 0 {
					if components[section] != nil {
						t.Errorf("%s must omit addon overrides", section)
					}
				} else if !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %v, want %v", section, got, want)
				}
			}
		})
	}
	for _, retired := range []string{"recommended", "app"} {
		cmd := exec.Command("helm", "template", "../bootstrap", "--set", "bootstrap.install="+retired)
		if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "schema") {
			t.Fatalf("removed profile %q must fail schema validation: %v\n%s", retired, err, output)
		}
	}
}

// TestNstanceBootstrapAndSleepConfig verifies rendered bootstrap references,
// sleep settings, and placement compatible with the final control-plane node.
func TestNstanceBootstrapAndSleepConfig(t *testing.T) {
	for _, mode := range []string{"default", "disabled", "custom"} {
		args := []string{"--namespace", "platform-nstance-operator", "-f", "values/nstance-operator.yaml",
			"--set", "registrationSecret.name=seed-nonce", "--set", "clusterCA.name=seed-ca"}
		if mode == "disabled" {
			args = append(args, "--set", "config.sleep.enabled=false")
		}
		if mode == "custom" {
			args = append(args, "--set", "config.sleep.inactivity=47m",
				"--set", "config.sleep.activityWindows.api=13m", "--set", "config.sleep.activityWindows.ingress=29m",
				"--set", "config.sleep.cronLookAhead=17m", "--set", "config.sleep.wakeLead=3m", "--set", "config.sleep.reconcilePeriod=45s")
		}
		for _, doc := range chartDocuments(t, render(t, "../charts/nstance-operator", args...)) {
			switch doc["kind"] {
			case "Secret", "SecretProviderClass", "SecretProviderBinding":
				t.Errorf("registration must not render credentials or use CSI: %s", doc["kind"])
			case "ConfigMap":
				var config map[string]any
				if err := yaml.Unmarshal([]byte(chartMap(doc, "data")["config.yaml"].(string)), &config); err != nil {
					t.Fatal(err)
				}
				if config["cluster_id"] != "test-cluster" || config["tenant"] != "test-tenant" {
					t.Errorf("wrong cluster/tenant: %v", config)
				}
				shard := chartMap(config, "shards", "test-shard")
				if shard["registration_addr"] != "127.0.0.1:8992" || shard["operator_addr"] != "127.0.0.1:8993" {
					t.Errorf("registration and operator endpoints changed: %v", shard)
				}
				want := map[string]any{"enabled": mode != "disabled", "remaining_nodes": 1, "inactivity": "30m", "activity_windows": map[string]any{}, "cron_look_ahead": "10m", "wake_lead": "2m", "reconcile_period": "1m"}
				if mode == "custom" {
					want = map[string]any{"enabled": true, "remaining_nodes": 1, "inactivity": "47m", "activity_windows": map[string]any{"api": "13m", "ingress": "29m"}, "cron_look_ahead": "17m", "wake_lead": "3m", "reconcile_period": "45s"}
				}
				if !reflect.DeepEqual(config["sleep"], want) {
					t.Errorf("%s sleep policy = %v, want %v", mode, config["sleep"], want)
				}
			case "Deployment":
				if chartMap(doc, "spec")["replicas"] != 2 {
					t.Error("normal-operation leader-election redundancy must remain enabled")
				}
				pod := chartMap(doc, "spec", "template", "spec")
				if chartMap(pod, "nodeSelector")["node-role.kubernetes.io/control-plane"] != "" {
					t.Error("operator must be placed on a control-plane node")
				}
				anti := chartMap(pod, "affinity", "podAntiAffinity")
				if anti["requiredDuringSchedulingIgnoredDuringExecution"] != nil || len(anti["preferredDuringSchedulingIgnoredDuringExecution"].([]any)) == 0 {
					t.Error("spreading must be preferred so both replicas can run on the final node")
				}
				tolerations := pod["tolerations"].([]any)
				for _, key := range []string{"node-role.kubernetes.io/control-plane", "node-role.kubernetes.io/master"} {
					if !slices.ContainsFunc(tolerations, func(raw any) bool {
						tol := raw.(map[string]any)
						return tol["key"] == key && tol["operator"] == "Exists" && tol["effect"] == "NoSchedule"
					}) {
						t.Errorf("missing %s toleration", key)
					}
				}
				env := pod["containers"].([]any)[0].(map[string]any)["env"].([]any)
				values := map[string]map[string]any{}
				for _, raw := range env {
					entry := raw.(map[string]any)
					values[entry["name"].(string)] = entry
				}
				if values["NSTANCE_NONCE_SECRET"]["value"] != "seed-nonce" || values["NSTANCE_CA_CONFIGMAP"]["value"] != "seed-ca" || chartMap(values["NSTANCE_NODE_NAME"], "valueFrom", "fieldRef")["fieldPath"] != "spec.nodeName" {
					t.Errorf("wrong bootstrap/Downward API env: %v", values)
				}
			}
		}
	}
}

// TestNstanceNonceReference verifies Helm rejects unsupported nonce keys and
// empty Secret names or names that collide with managed credentials.
func TestNstanceNonceReference(t *testing.T) {
	for _, override := range []string{"registrationSecret.key=other", "registrationSecret.name=", "registrationSecret.name=nstance-operator-cert"} {
		out, err := exec.Command("helm", "template", "test", "../charts/nstance-operator", "-f", "values/nstance-operator.yaml", "--set", override).CombinedOutput()
		if err == nil {
			t.Errorf("accepted unsupported/unsafe nonce reference %s: %s", override, out)
		}
	}
}

// TestNstanceValuesSchema verifies supported overrides, conditional configuration
// requirements, and rejection of unsupported duration syntax and unknown fields.
func TestNstanceValuesSchema(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		allowed bool
	}{
		{"zero values defaulted by Nstance", []string{"--set", "replicaCount=0,config.sleep.remainingNodes=0", "--set-string", "config.sleep.inactivity=0,config.sleep.reconcilePeriod=0"}, true},
		{"duration syntax and logging verbosity", []string{"--set-string", "config.sleep.inactivity=1h30m,config.sleep.wakeLead=.5s,config.sleep.activityWindows.api=0,logLevel=2"}, true},
		{"external config", []string{"--set", "operatorConfigMap.enabled=false,config.shards=null"}, true},
		{"disabled config and admission", []string{"--set", "operatorConfigMap.enabled=false,sleepAdmissionPolicy.enabled=false,config.shards=null,config.clusterID=,config.tenant="}, true},
		{"Kubernetes overrides", []string{"--set", "affinity=null,resources.limits=null,imagePullSecrets[0].name=registry,containerSecurityContext.capabilities.add[0]=NET_BIND_SERVICE"}, true},
		{"duration units", []string{"--set", "config.sleep.inactivity=1d"}, false},
		{"missing shard endpoint", []string{"--set", "config.shards.test-shard.operator_addr=null"}, false},
		{"empty shards", []string{"--set", "config.shards=null"}, false},
		{"admission still needs identity", []string{"--set", "operatorConfigMap.enabled=false,config.clusterID="}, false},
		{"CA enabled without certificate", []string{"--set", "clusterCA.enabled=true"}, false},
		{"unknown chart field", []string{"--set", "replicas=3"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := []string{"template", "test", "../charts/nstance-operator", "-f", "values/nstance-operator.yaml"}
			out, err := exec.Command("helm", append(args, test.args...)...).CombinedOutput()
			if (err == nil) != test.allowed {
				t.Fatalf("allowed=%v, want %v: %v\n%s", err == nil, test.allowed, err, out)
			}
		})
	}
}

// TestNstanceRBAC verifies the operator's bound permissions support reconciliation
// while restricting mutations by namespace, resource, and name.
func TestNstanceRBAC(t *testing.T) {
	docs := chartDocuments(t, render(t, "../charts/nstance-operator", "--namespace", "platform-nstance-operator", "-f", "values/nstance-operator.yaml"))
	// Evaluate the permissions bound to the operator, including Role namespace
	// and resourceNames. Do not accidentally include its separate workload SA.
	bound := map[string]bool{}
	for _, doc := range docs {
		if doc["kind"] != "RoleBinding" && doc["kind"] != "ClusterRoleBinding" {
			continue
		}
		for _, raw := range doc["subjects"].([]any) {
			subject := raw.(map[string]any)
			if subject["kind"] == "ServiceAccount" && subject["name"] == "test-nstance-operator" && subject["namespace"] == "platform-nstance-operator" {
				ref := chartMap(doc, "roleRef")
				bound[ref["kind"].(string)+"/"+ref["name"].(string)] = true
			}
		}
	}
	allowed := func(group, resource, verb, namespace, name string) bool {
		for _, doc := range docs {
			if doc["kind"] != "Role" && doc["kind"] != "ClusterRole" {
				continue
			}
			if !bound[doc["kind"].(string)+"/"+chartMap(doc, "metadata")["name"].(string)] {
				continue
			}
			if doc["kind"] == "Role" && chartMap(doc, "metadata")["namespace"] != namespace {
				continue
			}
			for _, raw := range doc["rules"].([]any) {
				rule := raw.(map[string]any)
				contains := func(key, value string) bool {
					entries, _ := rule[key].([]any)
					return slices.Contains(entries, any(value)) || slices.Contains(entries, any("*"))
				}
				if contains("apiGroups", group) && contains("resources", resource) && contains("verbs", verb) && (rule["resourceNames"] == nil || contains("resourceNames", name)) {
					return true
				}
			}
		}
		return false
	}
	for _, test := range []struct {
		group, resource, verb, namespace, name string
		want                                   bool
	}{
		{"batch", "jobs", "list", "", "", true}, {"batch", "cronjobs", "watch", "", "", true},
		{"", "nodes", "get", "", "node", true}, {"", "pods", "list", "other", "", true},
		{"", "events", "create", "platform-nstance-operator", "", true},
		{"coordination.k8s.io", "leases", "update", "platform-nstance-operator", "nstance-operator-leader-election", true},
		{"cluster.x-k8s.io", "clusters", "patch", "platform-nstance-operator", "test-cluster--test-tenant", true},
		{"cluster.x-k8s.io", "clusters", "patch", "platform-nstance-operator", "other", false},
		{"cluster.x-k8s.io", "clusters", "patch", "other", "test-cluster--test-tenant", false},
		{"infrastructure.cluster.x-k8s.io", "nstancemachines/status", "patch", "platform-nstance-operator", "machine", true},
		{"", "secrets", "delete", "platform-nstance-operator", "nstance-operator-nonce", true},
		{"", "secrets", "delete", "other", "nstance-operator-nonce", false},
		{"", "secrets", "delete", "platform-nstance-operator", "nstance-operator-cert", false},
		{"", "secrets", "update", "platform-nstance-operator", "nstance-operator-cert", true},
		{"", "secrets", "patch", "platform-nstance-operator", "unrelated", false},
		{"", "secrets", "create", "other", "unrelated", false},
		{"", "serviceaccounts/token", "create", "platform-nstance-operator", "test-nstance-operator-capi-workload", true},
		{"", "serviceaccounts/token", "create", "platform-nstance-operator", "unrelated", false},
		{"batch", "jobs", "delete", "default", "job", false},
		{"apiextensions.k8s.io", "customresourcedefinitions", "patch", "", "unrelated", false},
		{"rbac.authorization.k8s.io", "clusterroles", "update", "", "unrelated", false},
		{"", "namespaces", "delete", "", "default", false},
	} {
		if got := allowed(test.group, test.resource, test.verb, test.namespace, test.name); got != test.want {
			t.Errorf("%s %s/%s %s/%s allowed=%v, want %v", test.verb, test.group, test.resource, test.namespace, test.name, got, test.want)
		}
	}
}

// TestNstanceProviderOverlay verifies AWS/Google release wiring, values forwarding
// through downstream chart renders, and generic bootstrap exclusions.
func TestNstanceProviderOverlay(t *testing.T) {
	for _, provider := range []string{"aws", "gcp"} {
		t.Run(provider, func(t *testing.T) {
			args := []string{"--namespace", "platform-components", "-f", "values/platform-components.yaml"}
			if provider == "gcp" {
				args = append(args, "--set", "platform.components.apps.secrets-store-csi-provider-aws.enabled=false",
					"--set", "platform.components.apps.secrets-store-csi-provider-gcp.enabled=true",
					"--set", "platform.components.values.podplane-operator.podplane.operator.config.secrets.defaultProvider=google",
					"--set", "platform.components.values.podplane-operator.podplane.operator.config.secrets.providers.google.kind=gcp",
					"--set", "platform.components.values.podplane-operator.podplane.operator.config.secrets.providers.google.projectID=test-project")
			}
			releases := map[string]map[string]any{}
			for _, doc := range chartDocuments(t, render(t, "../charts/platform-components", args...)) {
				if doc["kind"] == "HelmRelease" {
					releases[chartMap(doc, "metadata")["name"].(string)] = doc
				}
			}
			for app, deps := range map[string][]string{
				"secrets-store-csi-driver":               {"secrets-store-csi-driver-crds"},
				"secrets-store-csi-provider-" + provider: {"secrets-store-csi-driver"},
				"podplane-operator":                      {"podplane-operator-crds", "secrets-store-csi-driver", "secrets-store-csi-provider-" + provider},
				"cluster-api":                            {"cluster-api-crds", "podplane-operator"},
				"nstance-operator":                       {"nstance-operator-crds", "cluster-api"},
			} {
				spec := chartMap(releases[app], "spec")
				if chartMap(spec, "install")["crds"] != "Skip" || chartMap(spec, "upgrade")["crds"] != "Skip" {
					t.Errorf("%s must skip application CRDs", app)
				}
				var actual []string
				for _, raw := range spec["dependsOn"].([]any) {
					dep := raw.(map[string]any)
					name := dep["name"].(string)
					if releases[name] == nil || chartMap(releases[name], "metadata")["namespace"] != dep["namespace"] {
						t.Errorf("%s dependency %s must resolve to an enabled release in its namespace", app, name)
					}
					actual = append(actual, name)
				}
				if !reflect.DeepEqual(actual, deps) {
					t.Errorf("%s dependencies=%v, want %v", app, actual, deps)
				}
				values, err := yaml.Marshal(spec["values"])
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "values.yaml")
				if err := os.WriteFile(path, values, 0o600); err != nil {
					t.Fatal(err)
				}
				rendered := render(t, "../charts/"+app, "--namespace", chartMap(releases[app], "metadata")["namespace"].(string), "-f", path)
				if strings.Contains(rendered, "kind: CustomResourceDefinition") {
					t.Errorf("%s app must not render CRDs", app)
				}
				if app == "nstance-operator" && !strings.Contains(rendered, "registry.example/mirror/ghcr.io/nstance-dev/nstance-operator:") {
					t.Error("Nstance must use the configured image mirror")
				}
			}
			for _, name := range []string{"cluster-api-crds", "nstance-operator-crds"} {
				if chartMap(releases[name], "metadata")["namespace"] != "platform-cluster" || !strings.Contains(render(t, "../charts/"+name), "kind: CustomResourceDefinition") {
					t.Errorf("%s must manage CRDs separately in platform-cluster", name)
				}
			}
		})
	}
	var defaults map[string]any
	body, err := os.ReadFile("../charts/platform-components/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(body, &defaults); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cluster-api", "cluster-api-crds", "nstance-operator", "nstance-operator-crds"} {
		section := "apps"
		if strings.HasSuffix(name, "-crds") {
			section = "crds"
		}
		entry := chartMap(defaults, "platform", "components", section, name)
		if entry["core"] != true || entry["enabled"] != false {
			t.Errorf("%s must be protected but disabled by default", name)
		}
	}
	// "none" means no bootstrap chart at all; it is not a bootstrap install set.
	for _, install := range []string{"minimal", "platform", "agentic", "all"} {
		rendered := render(t, "../bootstrap", "--set", "bootstrap.install="+install)
		for _, name := range []string{"cluster-api", "nstance-operator"} {
			if strings.Contains(rendered, name+":") {
				t.Errorf("generic %s bootstrap enabled provider overlay %s", install, name)
			}
		}
	}
}

// TestNstanceSleepAdmission evaluates the rendered CEL policy for sleep requests,
// checkpoints, and recovery, including custom cluster and ServiceAccount names.
// RBAC, not this policy, authorizes Cluster writers.
func TestNstanceSleepAdmission(t *testing.T) {
	for _, custom := range []bool{false, true} {
		namespace, cluster, operator := "platform-nstance-operator", "test-cluster--test-tenant", "test-nstance-operator"
		args := []string{"--namespace", namespace, "-f", "values/nstance-operator.yaml"}
		if custom {
			namespace, cluster, operator = "custom-namespace", "other-cluster--other-tenant", "custom-operator"
			args = append(args, "--namespace", namespace, "--set", "config.clusterID=other-cluster", "--set", "config.tenant=other-tenant", "--set", "serviceAccount.name="+operator)
		}
		t.Run(namespace, func(t *testing.T) {
			var policy, binding map[string]any
			var policyName string
			for _, doc := range chartDocuments(t, render(t, "../charts/nstance-operator", args...)) {
				switch doc["kind"] {
				case "ValidatingAdmissionPolicy":
					policy, policyName = chartMap(doc, "spec"), chartMap(doc, "metadata")["name"].(string)
				case "ValidatingAdmissionPolicyBinding":
					binding = chartMap(doc, "spec")
				}
			}
			if policy == nil || binding == nil || binding["policyName"] != policyName || policy["failurePolicy"] != "Fail" || !slices.Contains(binding["validationActions"].([]any), any("Deny")) {
				t.Fatal("Nstance must own a bound, fail-closed sleep policy")
			}
			rules := chartMap(policy, "matchConstraints")["resourceRules"].([]any)
			if len(rules) != 1 || rules[0].(map[string]any)["scope"] != "Namespaced" {
				t.Fatal("sleep policy must match namespaced CAPI Cluster writes")
			}
			for key, values := range map[string][]string{"apiGroups": {"cluster.x-k8s.io"}, "resources": {"clusters"}, "apiVersions": {"*"}, "operations": {"CREATE", "UPDATE"}} {
				for _, value := range values {
					if !slices.Contains(rules[0].(map[string]any)[key].([]any), any(value)) {
						t.Errorf("sleep policy missing %s=%s", key, value)
					}
				}
			}
			env, err := cel.NewEnv(ext.Strings(ext.StringsVersion(2)), cel.Variable("object", cel.DynType), cel.Variable("oldObject", cel.DynType), cel.Variable("request", cel.DynType), cel.Variable("variables", cel.DynType))
			if err != nil {
				t.Fatal(err)
			}
			compile := func(expression string) cel.Program {
				t.Helper()
				ast, issues := env.Compile(expression)
				if issues.Err() != nil {
					t.Fatal(issues.Err())
				}
				program, err := env.Program(ast)
				if err != nil {
					t.Fatal(err)
				}
				return program
			}
			var conditions, validations []cel.Program
			for _, raw := range policy["matchConditions"].([]any) {
				conditions = append(conditions, compile(raw.(map[string]any)["expression"].(string)))
			}
			vars := policy["variables"].([]any)
			programs := make([]cel.Program, len(vars))
			for i, raw := range vars {
				programs[i] = compile(raw.(map[string]any)["expression"].(string))
			}
			for _, raw := range policy["validations"].([]any) {
				validations = append(validations, compile(raw.(map[string]any)["expression"].(string)))
			}
			object := func(annotations map[string]string) any {
				metadata := map[string]any{"name": cluster, "namespace": namespace}
				if annotations != nil {
					metadata["annotations"] = annotations
				}
				return map[string]any{"metadata": metadata}
			}
			request := map[string]string{"nstance.dev/sleep-request": "2026-10-12T03:15:00.123456789Z"}
			force := map[string]string{"nstance.dev/sleep-request": "force:2026-10-12T03:16:00Z"}
			checkpoint := map[string]string{"nstance.dev/sleep-request": request["nstance.dev/sleep-request"], "nstance.dev/sleep-progress": `{"request":"2026-10-12T03:15:00.123456789Z","phase":"finalizing","final_shard":"zone-a"}`}
			compensating := map[string]string{"nstance.dev/sleep-request": request["nstance.dev/sleep-request"], "nstance.dev/sleep-progress": `{"request":"2026-10-12T03:15:00.123456789Z","phase":"compensating","final_shard":"zone-a"}`}
			operatorUser := "system:serviceaccount:" + namespace + ":" + operator
			otherCluster := object(force).(map[string]any)
			chartMap(otherCluster, "metadata")["name"] = "unmanaged-cluster"
			otherNamespace := object(force).(map[string]any)
			chartMap(otherNamespace, "metadata")["namespace"] = "unmanaged-namespace"
			for _, test := range []struct {
				name, username, group string
				old, next             any
				want                  bool
			}{
				{"normal request", "alice", "podplane:admins", object(nil), object(request), true},
				{"forced request", "alice", "podplane:admins", object(nil), object(force), true},
				{"no extra human authorization", "bob", "custom-cluster-writers", object(nil), object(request), true},
				{"annotated create", "bob", "custom-cluster-writers", nil, object(force), true},
				{"plain create", "bob", "custom-cluster-writers", nil, object(nil), true},
				{"offset timestamp", "bob", "custom-cluster-writers", object(nil), object(map[string]string{"nstance.dev/sleep-request": "2026-10-12T10:05:03.123456789+05:30"}), true},
				{"invalid timestamp", "alice", "podplane:admins", object(nil), object(map[string]string{"nstance.dev/sleep-request": "now"}), false},
				{"invalid date", "alice", "podplane:admins", object(nil), object(map[string]string{"nstance.dev/sleep-request": "2026-02-30T00:00:00Z"}), false},
				{"missing forced timestamp", "alice", "podplane:admins", nil, object(map[string]string{"nstance.dev/sleep-request": "force:"}), false},
				{"empty request", "alice", "podplane:admins", object(nil), object(map[string]string{"nstance.dev/sleep-request": ""}), false},
				{"replace active request", "alice", "podplane:admins", object(request), object(force), false},
				{"remove active request", "alice", "podplane:admins", object(request), object(nil), false},
				{"request removal retaining checkpoint", operatorUser, "system:serviceaccounts", object(checkpoint), object(map[string]string{"nstance.dev/sleep-progress": checkpoint["nstance.dev/sleep-progress"]}), false},
				{"operator cannot replace transaction", operatorUser, "system:serviceaccounts", object(checkpoint), object(force), false},
				{"unrelated change", "alice", "podplane:admins", object(checkpoint), object(checkpoint), true},
				{"admin cannot manufacture progress", "alice", "podplane:admins", object(request), object(checkpoint), false},
				{"admin cannot replace progress", "alice", "podplane:admins", object(checkpoint), object(compensating), false},
				{"admin cannot delete progress", "alice", "podplane:admins", object(checkpoint), object(request), false},
				{"empty progress still operator-owned", "alice", "podplane:admins", object(request), object(map[string]string{"nstance.dev/sleep-request": request["nstance.dev/sleep-request"], "nstance.dev/sleep-progress": ""}), false},
				{"operator automatic request", operatorUser, "system:serviceaccounts", object(nil), object(request), true},
				{"operator checkpoint", operatorUser, "system:serviceaccounts", object(request), object(checkpoint), true},
				{"operator recovery checkpoint", operatorUser, "system:serviceaccounts", object(checkpoint), object(compensating), true},
				{"operator cleanup after recovery", operatorUser, "system:serviceaccounts", object(checkpoint), object(nil), true},
				{"operator rejects malformed request", operatorUser, "system:serviceaccounts", object(map[string]string{"nstance.dev/sleep-request": "bad"}), object(nil), true},
				{"unrelated update with preexisting malformed request", "alice", "podplane:admins", object(map[string]string{"nstance.dev/sleep-request": "bad"}), object(map[string]string{"nstance.dev/sleep-request": "bad"}), true},
				{"wrong namespace serviceaccount", "system:serviceaccount:other:" + operator, "system:serviceaccounts", object(request), object(checkpoint), false},
				{"other cluster unaffected", "alice", "podplane:admins", object(request), otherCluster, true},
				{"other namespace unaffected", "alice", "podplane:admins", object(request), otherNamespace, true},
			} {
				t.Run(test.name, func(t *testing.T) {
					variables := map[string]any{}
					input := map[string]any{"object": test.next, "oldObject": test.old, "request": map[string]any{"userInfo": map[string]any{"username": test.username, "groups": []string{test.group}}}, "variables": variables}
					for _, condition := range conditions {
						result, _, err := condition.Eval(input)
						if err != nil {
							t.Fatal(err)
						}
						if result != types.True {
							if !test.want {
								t.Fatal("policy unexpectedly skipped the managed Cluster")
							}
							return
						}
					}
					for i, program := range programs {
						result, _, err := program.Eval(input)
						if err != nil {
							t.Fatal(err)
						}
						variables[vars[i].(map[string]any)["name"].(string)] = result.Value()
					}
					allowed := true
					for _, program := range validations {
						result, _, err := program.Eval(input)
						// Evaluation errors deny requests under failurePolicy: Fail.
						allowed = allowed && err == nil && result == types.True
					}
					if allowed != test.want {
						t.Errorf("allowed=%v, want %v", allowed, test.want)
					}
				})
			}
		})
	}
	for _, doc := range chartDocuments(t, render(t, "../charts/nstance-operator", "-f", "values/nstance-operator.yaml", "--set", "sleepAdmissionPolicy.enabled=false")) {
		if doc["kind"] == "ValidatingAdmissionPolicy" || doc["kind"] == "ValidatingAdmissionPolicyBinding" {
			t.Error("disabled sleep admission still renders resources")
		}
	}
}
