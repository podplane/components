// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSourceGatewayContract checks workload TLS and encryption-key binding wiring,
// always using the cluster default provider.
func TestSourceGatewayContract(t *testing.T) {
	rendered := render(t, "../charts/sourcegateway", "--namespace", "platform-sourcegateway")
	// gVisor cannot join a Pod user namespace, so Source Gateway must run non-root
	// without one rather than as UID 0 with hostUsers=false.
	for _, want := range []string{"hostUsers: true", "runAsUser: 65532", "fsGroup: 65532", "serviceAccountName: sourcegateway", "secretProviderClass: \"sourcegateway\"", "signerName: certificates.podplane.dev/workload", "keyType: ED25519", "keyPath: tls.key", "certificateChainPath: tls.crt", "certificates.podplane.dev/mode: service", "certificates.podplane.dev/service: sourcegateway"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(rendered, "runAsUser: 0\n") {
		t.Error("Source Gateway must not run as UID 0 without a user namespace")
	}
	bindings, pvcs := 0, 0
	for _, doc := range chartDocuments(t, rendered) {
		switch doc["kind"] {
		case "SecretProviderClass", "CustomResourceDefinition":
			t.Errorf("unexpected resource %s", doc["kind"])
		case "PersistentVolumeClaim":
			pvcs++
			spec := chartMap(doc, "spec")
			if spec["storageClassName"] != nil || !reflect.DeepEqual(spec["accessModes"], []any{"ReadWriteOnce"}) || chartMap(spec, "resources", "requests")["storage"] != "30Gi" {
				t.Fatal("Source Gateway must use the default class with upstream capacity and access mode", spec)
			}
		case "SecretProviderBinding":
			bindings++
			if doc["apiVersion"] != "secrets.podplane.dev/v1beta1" || chartMap(doc, "metadata")["name"] != "sourcegateway" || chartMap(doc, "metadata")["namespace"] != "platform-sourcegateway" {
				t.Fatalf("incorrect binding: %v", doc)
			}
			if _, ok := chartMap(doc, "spec")["providerName"]; ok {
				t.Fatal("default binding must omit providerName")
			}
			items := chartMap(doc, "spec")["items"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["key"] != "encryption-key" || items[0].(map[string]any)["path"] != "encryption-key" {
				t.Fatalf("incorrect binding items: %v", items)
			}
		}
	}
	if bindings != 1 || pvcs != 1 {
		t.Fatalf("got %d bindings and %d PVCs, want one of each", bindings, pvcs)
	}
	override := render(t, "../charts/sourcegateway", "--set", "platform.sourceGateway.providerName=alternate")
	for _, doc := range chartDocuments(t, override) {
		if doc["kind"] == "SecretProviderBinding" {
			if _, ok := chartMap(doc, "spec")["providerName"]; ok {
				t.Fatal("obsolete provider override must not replace the cluster default")
			}
		}
	}
}

// TestAgentRouterContract checks that the upstream chart patch uses Podplane
// workload TLS and CA injection without generating a separate serving secret,
// and that Agent Router's extension runs on its own Envoy Gateway controller.
func TestAgentRouterContract(t *testing.T) {
	rendered := render(t, "../charts/agent-router", "--namespace", "platform-agent-router")
	for _, want := range []string{"signerName: certificates.podplane.dev/workload", "certificates.podplane.dev/service: agent-router", "name: certificates.podplane.dev:workload:roots", "path: ca.crt", "--envoyGatewayNamespace=platform-agent-router"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q", want)
		}
	}
	secrets, webhooks := 0, 0
	controllerName, classController := "", ""
	for _, doc := range chartDocuments(t, rendered) {
		switch doc["kind"] {
		case "CustomResourceDefinition", "Certificate", "Issuer":
			t.Errorf("unexpected %s", doc["kind"])
		case "GatewayClass":
			if chartMap(doc, "metadata")["name"] != "platform-agent-router" {
				t.Errorf("unexpected GatewayClass %v", chartMap(doc, "metadata"))
			}
			classController, _ = chartMap(doc, "spec")["controllerName"].(string)
		case "ConfigMap":
			if chartMap(doc, "metadata")["name"] != "envoy-gateway-config" {
				continue
			}
			var config map[string]any
			if err := yaml.Unmarshal([]byte(chartMap(doc, "data")["envoy-gateway.yaml"].(string)), &config); err != nil {
				t.Fatal(err)
			}
			controllerName, _ = chartMap(config, "gateway")["controllerName"].(string)
			wantService := map[string]any{
				"fqdn": map[string]any{"hostname": "agent-router.platform-agent-router.svc.cluster.local", "port": 1063},
				"tls":  map[string]any{"certificateRef": map[string]any{"kind": "Secret", "name": "workload-ca"}},
			}
			if !reflect.DeepEqual(chartMap(config, "extensionManager", "service"), wantService) || chartMap(config, "extensionApis")["enableBackend"] != true {
				t.Errorf("Agent Router extension missing from its controller: %v", config)
			}
			if chartMap(config, "extensionApis")["enableSDSSecretRef"] != true {
				t.Error("Agent Router's controller must resolve SDS references")
			}
		case "Secret":
			secrets++
			switch chartMap(doc, "metadata")["name"] {
			case "platform-agent-router-serving":
				want := map[string]any{"url": "unix:///var/run/podplane-sds/sds.sock", "secretName": "serving"}
				if doc["type"] != "gateway.envoyproxy.io/sds" || doc["data"] != nil || !reflect.DeepEqual(doc["stringData"], want) {
					t.Fatal("serving Secret must contain only the SDS socket and resource name")
				}
			case "agent-router-mcp-session-encryption":
			default:
				t.Fatalf("unexpected Secret: %v", chartMap(doc, "metadata"))
			}
		case "Gateway":
			listeners := chartMap(doc, "spec")["listeners"].([]any)
			if len(listeners) != 1 {
				t.Fatal("Agent Router must have exactly one HTTPS listener")
			}
			listener := listeners[0].(map[string]any)
			wantTLS := map[string]any{"mode": "Terminate", "certificateRefs": []any{map[string]any{"kind": "Secret", "name": "platform-agent-router-serving"}}}
			if listener["protocol"] != "HTTPS" || listener["port"] != 443 || !reflect.DeepEqual(listener["tls"], wantTLS) {
				t.Errorf("Agent Router must terminate HTTPS using SDS: %v", listener)
			}
			// Routes see every agent's requests, so only administrator-labelled
			// namespaces (guarded by platform-agent-router-routes-vap) may attach.
			wantRoutes := map[string]any{"namespaces": map[string]any{"from": "Selector", "selector": map[string]any{"matchLabels": map[string]any{"agent-router.podplane.dev/routes": "allowed"}}}}
			if !reflect.DeepEqual(listener["allowedRoutes"], wantRoutes) {
				t.Errorf("routes must be limited to labelled namespaces: %v", listener["allowedRoutes"])
			}
		case "MutatingWebhookConfiguration":
			webhooks++
			if chartMap(doc, "metadata")["name"] != "envoy-ai-gateway-gateway-pod-mutator.platform-agent-router" || chartMap(doc, "metadata", "annotations")["certificates.podplane.dev/inject-ca-from"] != "workload" {
				t.Errorf("incorrect workload trust injection: %v", doc)
			}
			webhook := doc["webhooks"].([]any)[0].(map[string]any)
			if chartMap(webhook, "clientConfig")["caBundle"] != nil || chartMap(webhook, "clientConfig", "service")["name"] != "agent-router" {
				t.Errorf("incorrect webhook: %v", webhook)
			}
			// The webhook fails closed, so it must not gate ingress Envoy Pods.
			if chartMap(webhook, "objectSelector", "matchLabels")["gateway.envoyproxy.io/owning-gateway-name"] != "platform-agent-router-gateway" {
				t.Errorf("webhook must select only Agent Router's proxy Pods: %v", webhook["objectSelector"])
			}
		}
	}
	// One webhook: the Envoy Gateway topology injector must stay disabled, since
	// in GatewayNamespace mode it would intercept every Pod binding.
	if secrets != 2 || webhooks != 1 {
		t.Fatalf("got %d Secrets and %d webhooks", secrets, webhooks)
	}
	// The ingress controller uses Envoy Gateway's default controllerName.
	if controllerName == "" || controllerName != classController || controllerName == "gateway.envoyproxy.io/gatewayclass-controller" {
		t.Errorf("GatewayClass controller %q must be this chart's dedicated controller %q", classController, controllerName)
	}
	// AI/MCP routes get their own internal proxy fleet, separate from the
	// ingress DaemonSet and its host ports.
	for _, want := range []string{"kind: Gateway\n", "gatewayClassName: platform-agent-router", "mergeType: Replace", "envoyDeployment:", "image: \"docker.io/envoyproxy/envoy:distroless-v1.39.1\"", "type: ClusterIP", "name: platform-agent-router-extproc-gvisor-map", "reinvocationPolicy: IfNeeded"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, forbidden := range []string{"envoyDaemonSet:", "hostPort:", "extension-tls", "extension-trust"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("unexpected %q", forbidden)
		}
	}
}

// TestAgentRouterServingIdentity checks the proxy's Service identity, projection,
// and SDS wiring in both the platform namespace and an alternate namespace.
func TestAgentRouterServingIdentity(t *testing.T) {
	for _, namespace := range []string{"platform-agent-router", "alternate-router"} {
		t.Run(namespace, func(t *testing.T) {
			for _, doc := range chartDocuments(t, render(t, "../charts/agent-router", "--namespace", namespace)) {
				if doc["kind"] != "EnvoyProxy" {
					continue
				}
				provider := chartMap(doc, "spec", "provider", "kubernetes")
				service := chartMap(provider, "envoyService")
				if service["name"] != "platform-agent-router-gateway" || service["type"] != "ClusterIP" {
					t.Fatal("proxy must have a stable internal Service identity")
				}
				template := chartMap(provider, "envoyDeployment", "patch", "value", "spec", "template")
				if chartMap(template, "metadata", "annotations")["dev.gvisor.spec.mount.podplane-sds.share"] != "pod" {
					t.Error("SDS socket must be shared between gVisor containers")
				}
				pod := chartMap(template, "spec")
				var projection, sidecar map[string]any
				for _, raw := range pod["volumes"].([]any) {
					volume := raw.(map[string]any)
					if volume["name"] == "serving-certificate" {
						projection = chartMap(volume, "projected")
					}
				}
				wantSources := []any{map[string]any{"podCertificate": map[string]any{
					"signerName": "certificates.podplane.dev/workload", "keyType": "ECDSAP256", "credentialBundlePath": "serving",
					"userAnnotations": map[string]any{"certificates.podplane.dev/mode": "service", "certificates.podplane.dev/service": service["name"]},
				}}}
				if !reflect.DeepEqual(projection["sources"], wantSources) {
					t.Errorf("incorrect serving projection: %v", projection)
				}
				for _, raw := range pod["containers"].([]any) {
					container := raw.(map[string]any)
					if container["name"] == "podplane-sds" {
						sidecar = container
					}
				}
				wantArgs := []any{"sds", "--socket=/var/run/podplane-sds/sds.sock", "--certificates-dir=/var/run/podplane/serving",
					"--workload-certificate=serving=platform-agent-router-gateway." + namespace + ".svc.cluster.local"}
				if !reflect.DeepEqual(sidecar["args"], wantArgs) {
					t.Errorf("SDS must load the projected bundle for this Service: %v", sidecar["args"])
				}
				wantMounts := []any{
					map[string]any{"name": "podplane-sds", "mountPath": "/var/run/podplane-sds"},
					map[string]any{"name": "serving-certificate", "mountPath": "/var/run/podplane/serving", "readOnly": true},
				}
				if !reflect.DeepEqual(sidecar["volumeMounts"], wantMounts) {
					t.Error("SDS must mount the entire certificate directory so projection rotation remains visible")
				}
			}
		})
	}
}

// TestAgentRouterConnectionBuffer checks that one ClientTrafficPolicy targets the
// Agent Router Gateway with the default buffer limit or a configured override.
func TestAgentRouterConnectionBuffer(t *testing.T) {
	for _, limit := range []string{"50Mi", "8Mi"} {
		t.Run(limit, func(t *testing.T) {
			args := []string{"--namespace", "platform-agent-router"}
			if limit != "50Mi" {
				args = append(args, "--set", "platform.agentRouter.clientTrafficPolicy.connection.bufferLimit="+limit)
			}
			policies := 0
			for _, doc := range chartDocuments(t, render(t, "../charts/agent-router", args...)) {
				if doc["kind"] != "ClientTrafficPolicy" {
					continue
				}
				policies++
				spec := chartMap(doc, "spec")
				wantRefs := []any{map[string]any{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "platform-agent-router-gateway"}}
				if !reflect.DeepEqual(spec["targetRefs"], wantRefs) || chartMap(spec, "connection")["bufferLimit"] != limit {
					t.Errorf("expected buffer %s on the Agent Router Gateway, got %v", limit, spec)
				}
			}
			if policies != 1 {
				t.Fatalf("got %d ClientTrafficPolicies, want one", policies)
			}
		})
	}
}

// TestAgentRouterClientAuthentication checks agent client certificate validation:
// verified when given by default, required or off when configured, and exact or
// namespace-wide SPIFFE URI matching for allowed clients.
func TestAgentRouterClientAuthentication(t *testing.T) {
	const auth = "platform.agentRouter.clientAuthentication."
	xfcc := map[string]any{"xForwardedClientCert": map[string]any{"mode": "SanitizeSet", "certDetailsToAdd": []any{"URI"}}}
	ca := []any{map[string]any{"kind": "ConfigMap", "name": "workload-ca"}}
	for _, tc := range []struct {
		name    string
		args    []string
		tls     any
		headers any
	}{
		{name: "verified when given by default",
			tls:     map[string]any{"clientValidation": map[string]any{"mode": "VerifyIfGiven", "caCertificateRefs": ca}},
			headers: xfcc},
		{name: "disabled", args: []string{"--set", auth + "mode=Disabled"}},
		{name: "required", args: []string{"--set", auth + "mode=RequireAndVerify"},
			tls:     map[string]any{"clientValidation": map[string]any{"mode": "RequireAndVerify", "caCertificateRefs": ca}},
			headers: xfcc},
		{name: "allowed clients", args: []string{
			"--set", auth + "mode=RequireAndVerify", "--set", "platform.agentRouter.spiffeTrustDomain=td.example",
			"--set-json", auth + `allowedClients=[{"namespace":"team-a","serviceAccount":"research-agent"},{"namespace":"team-b"}]`,
		},
			tls: map[string]any{"clientValidation": map[string]any{"mode": "RequireAndVerify", "caCertificateRefs": ca,
				"subjectAltNames": map[string]any{"uris": []any{
					map[string]any{"type": "Exact", "value": "spiffe://td.example/ns/team-a/sa/research-agent"},
					map[string]any{"type": "Prefix", "value": "spiffe://td.example/ns/team-b/sa/"},
				}}}},
			headers: xfcc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--namespace", "platform-agent-router"}, tc.args...)
			for _, doc := range chartDocuments(t, render(t, "../charts/agent-router", args...)) {
				if doc["kind"] != "ClientTrafficPolicy" {
					continue
				}
				spec := chartMap(doc, "spec")
				if !reflect.DeepEqual(spec["tls"], tc.tls) || !reflect.DeepEqual(spec["headers"], tc.headers) {
					t.Errorf("tls = %v, headers = %v", spec["tls"], spec["headers"])
				}
			}
		})
	}
	for _, tc := range []struct{ name, set, want string }{
		{"unknown mode", auth + "mode=Optional", "mode must be"},
		{"clients while disabled", auth + "mode=Disabled," + auth + "allowedClients[0].namespace=team-a", "requires mode RequireAndVerify"},
		{"clients by default", "platform.agentRouter.spiffeTrustDomain=td.example," + auth + "allowedClients[0].namespace=team-a", "requires mode RequireAndVerify"},
		{"clients without trust domain", auth + "mode=RequireAndVerify," + auth + "allowedClients[0].namespace=team-a", "spiffeTrustDomain is required"},
		{"namespace with path", auth + "mode=RequireAndVerify,platform.agentRouter.spiffeTrustDomain=td.example," + auth + "allowedClients[0].namespace=team-a/sa/x", "must be a DNS label"},
		{"serviceAccount with path", auth + "mode=RequireAndVerify,platform.agentRouter.spiffeTrustDomain=td.example," + auth + "allowedClients[0].namespace=team-a," + auth + "allowedClients[0].serviceAccount=x/y", "must be a DNS subdomain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := exec.Command("helm", "template", "test", "../charts/agent-router", "--kube-version", "1.37.0", "--set", tc.set).CombinedOutput()
			if err == nil || !strings.Contains(string(out), tc.want) {
				t.Fatalf("expected %q render failure, got %v:\n%s", tc.want, err, out)
			}
		})
	}
}

// TestAgentRouterRoutePermissionPolicy checks that only administrators, or Flux
// for platform namespaces, may change which namespaces can attach routes.
func TestAgentRouterRoutePermissionPolicy(t *testing.T) {
	policy := renderValidatingPolicy(t, "../charts/agent-router", "platform-agent-router-routes-vap", "--namespace", "platform-agent-router")
	namespace := func(name string, labels map[string]any) map[string]any {
		return map[string]any{"metadata": map[string]any{"name": name, "labels": labels}}
	}
	granted := map[string]any{"agent-router.podplane.dev/routes": "allowed"}
	unrelated := map[string]any{"owner": "team-a"}
	flux := "system:serviceaccount:platform-fluxcd:helm-controller"
	for _, test := range []struct {
		name, operation, namespace string
		old, new                   map[string]any
		username, group            string
		want                       bool
	}{
		{"manager grants", "UPDATE", "team-a", unrelated, granted, "bob", "podplane:managers", false},
		{"manager creates granted namespace", "CREATE", "team-a", nil, granted, "bob", "podplane:managers", false},
		{"manager revokes", "UPDATE", "team-a", granted, unrelated, "bob", "podplane:managers", false},
		{"manager edits unrelated label", "UPDATE", "team-a", granted, map[string]any{"agent-router.podplane.dev/routes": "allowed", "owner": "team-a"}, "bob", "podplane:managers", true},
		{"admin grants", "UPDATE", "team-a", unrelated, granted, "alice", "podplane:admins", true},
		{"flux grants platform namespace", "CREATE", "platform-agent-router", nil, granted, flux, "system:serviceaccounts", true},
		{"flux cannot grant tenant namespace", "UPDATE", "team-a", unrelated, granted, flux, "system:serviceaccounts", false},
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

// TestAgenticGatewayPlatformContract checks gateway release wiring and image
// mirrors, and that the ingress Envoy Gateway never depends on Agent Router.
func TestAgenticGatewayPlatformContract(t *testing.T) {
	rendered := render(t, "../charts/platform-components", "-f", "values/platform-components.yaml", "--set", "platform.components.apps.sourcegateway.enabled=true,platform.components.apps.agent-router.enabled=true,platform.components.apps.envoy-gateway.enabled=true")
	for _, doc := range chartDocuments(t, rendered) {
		name := chartMap(doc, "metadata")["name"]
		if doc["kind"] == "Namespace" && (name == "platform-sourcegateway" || name == "platform-agent-router") {
			labels := chartMap(doc, "metadata", "labels")
			if labels["pod-security.kubernetes.io/enforce"] != "restricted" || labels["runtime.podplane.dev/runc"] != nil {
				t.Errorf("unsafe namespace %v", doc)
			}
		}
		if doc["kind"] != "HelmRelease" {
			continue
		}
		spec := chartMap(doc, "spec")
		switch name {
		case "sourcegateway":
			deps := map[string]string{}
			for _, dep := range spec["dependsOn"].([]any) {
				entry := dep.(map[string]any)
				deps[entry["name"].(string)] = entry["namespace"].(string)
			}
			for _, dep := range []string{"sourcegateway-crds", "podplane-operator", "secrets-store-csi-driver"} {
				if deps[dep] == "" {
					t.Errorf("missing dependency %s", dep)
				}
			}
			image := chartMap(spec, "values", "sourcegateway", "image")
			if image["repository"] != "registry.example/mirror/ghcr.io/sourcegateway/sourcegateway" {
				t.Errorf("source mirror %v", image)
			}
		case "agent-router":
			values := chartMap(spec, "values", "ai-gateway-helm")
			for part, suffix := range map[string]string{"controller": "controller", "extProc": "extproc"} {
				if chartMap(values, part, "image")["repository"] != "registry.example/mirror/docker.io/envoyproxy/ai-gateway-"+suffix {
					t.Errorf("agent mirror %v", values)
				}
			}
			for _, path := range [][]string{
				{"values", "platform", "agentRouter", "envoyProxy"},
				{"values", "ai-gateway-helm", "controller", "extraContainers", "controller-tls"},
				{"values", "gateway-helm", "global", "images", "envoyProxy"},
			} {
				if image := chartMap(spec, path...)["image"]; image != "registry.example/mirror/docker.io/envoyproxy/envoy:distroless-v1.39.1" {
					t.Errorf("agent Envoy proxy mirror at %v: %v", path, image)
				}
			}
			if image := chartMap(spec, "values", "gateway-helm", "global", "images", "envoyGateway")["image"]; image != "registry.example/mirror/docker.io/envoyproxy/gateway:v1.9.2" {
				t.Errorf("agent Envoy Gateway mirror %v", image)
			}
			if image := chartMap(spec, "values", "platform", "agentRouter", "sdsSidecar", "image")["repository"]; image != "registry.example/mirror/ghcr.io/podplane/operator" {
				t.Errorf("agent SDS mirror %v", image)
			}
			for _, dep := range spec["dependsOn"].([]any) {
				if dep.(map[string]any)["name"] == "envoy-gateway" {
					t.Error("Agent Router must not wait for the ingress Envoy Gateway release")
				}
			}
		case "envoy-gateway":
			values, err := yaml.Marshal(spec["values"])
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "values.yaml")
			if err := os.WriteFile(file, values, 0o600); err != nil {
				t.Fatal(err)
			}
			gateway := render(t, "../charts/envoy-gateway", "-f", file)
			for _, want := range []string{"enableSDSSecretRef: true", "type: GatewayNamespace", "signerName: certificates.podplane.dev/workload"} {
				if !strings.Contains(gateway, want) {
					t.Errorf("composed gateway configuration lost %q", want)
				}
			}
			if strings.Contains(gateway, "extensionManager") || strings.Contains(gateway, "agent-router") {
				t.Error("ingress Envoy Gateway must not call the Agent Router extension")
			}
		}
	}
	without := render(t, "../charts/platform-components", "-f", "values/platform-components.yaml", "--set", "platform.components.apps.envoy-gateway.enabled=true")
	if strings.Contains(without, "extensionManager") || strings.Contains(without, "name: agent-router") {
		t.Fatal("platform without Agent Router must not depend on its extension")
	}
}

// TestAgentRouterTrustSyncWiring verifies startup ordering and preserves explicit
// sync targets while selecting the CA Secret used by the extension client and
// the CA ConfigMap and trust domain used for agent client certificates.
func TestAgentRouterTrustSyncWiring(t *testing.T) {
	const syncPath = "platform.components.values.podplane-operator.podplane.operator.config.workloadCertificates.trustBundleSync"
	for _, tc := range []struct {
		name              string
		enabled           bool
		args              []string
		namespace, secret string
		configMapNS       string
		preconfigured     bool
	}{
		{name: "disabled"},
		{name: "enabled", enabled: true, namespace: "platform-agent-router", secret: "workload-ca", configMapNS: "platform-agent-router"},
		{name: "custom reference", enabled: true, namespace: "custom-trust", secret: "custom-ca", configMapNS: "custom-router", args: []string{
			"--set", "platform.components.apps.agent-router.namespace=custom-router",
			"--set", "platform.components.values.agent-router.gateway-helm.config.envoyGateway.extensionManager.service.tls.certificateRef.name=custom-ca",
			"--set", "platform.components.values.agent-router.gateway-helm.config.envoyGateway.extensionManager.service.tls.certificateRef.namespace=custom-trust",
		}},
		{name: "existing target", enabled: true, namespace: "platform-agent-router", secret: "workload-ca", configMapNS: "platform-agent-router", preconfigured: true, args: []string{
			"--set-json", syncPath + `={"createIfMissing":true,"resources":[{"kind":"ConfigMap","namespace":"other","name":"roots","key":"ca.crt"},{"kind":"Secret","namespace":"platform-agent-router","name":"workload-ca","key":"tls.crt","createIfMissing":false}]}`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"-f", "values/platform-components.yaml"}
			if tc.enabled {
				args = append(args, "--set", "platform.components.apps.agent-router.enabled=true")
			}
			args = append(args, tc.args...)
			found := false
			for _, doc := range chartDocuments(t, render(t, "../charts/platform-components", args...)) {
				if doc["kind"] != "HelmRelease" {
					continue
				}
				if chartMap(doc, "metadata")["name"] == "agent-router" {
					depends := false
					for _, entry := range chartMap(doc, "spec")["dependsOn"].([]any) {
						if entry.(map[string]any)["name"] == "podplane-operator" {
							depends = true
						}
					}
					if !depends {
						t.Fatal("Agent Router must wait for the operator to populate its CA")
					}
					if domain := chartMap(doc, "spec", "values", "platform", "agentRouter")["spiffeTrustDomain"]; domain != "test-cluster.example" {
						t.Errorf("Agent Router trust domain = %v, want the operator's", domain)
					}
				}
				if chartMap(doc, "metadata")["name"] != "podplane-operator" {
					continue
				}
				found = true
				sync := chartMap(doc, "spec", "values", "podplane", "operator", "config", "workloadCertificates", "trustBundleSync")
				if !tc.enabled {
					if len(sync) != 0 {
						t.Fatalf("disabled Agent Router added sync configuration: %v", sync)
					}
					continue
				}
				want := []any{}
				if tc.preconfigured {
					want = append(want, map[string]any{"kind": "ConfigMap", "namespace": "other", "name": "roots", "key": "ca.crt"})
					if sync["createIfMissing"] != true {
						t.Fatal("global creation policy was overwritten")
					}
				}
				want = append(want,
					map[string]any{"kind": "Secret", "namespace": tc.namespace, "name": tc.secret, "key": "tls.crt", "createIfMissing": !tc.preconfigured},
					map[string]any{"kind": "ConfigMap", "namespace": tc.configMapNS, "name": "workload-ca", "key": "ca.crt", "createIfMissing": true})
				if !reflect.DeepEqual(sync["resources"], want) {
					t.Fatalf("sync targets = %v, want %v", sync["resources"], want)
				}
			}
			if !found {
				t.Fatal("operator HelmRelease not found")
			}
		})
	}
}

// TestAgentRouterControllerTLS verifies native extension TLS and the remaining
// termination sidecar without breaking probes or ordinary value overrides.
func TestAgentRouterControllerTLS(t *testing.T) {
	for _, name := range []string{"agent-router", "custom-router"} {
		t.Run(name, func(t *testing.T) {
			const namespace = "alternate-router"
			const image = "registry.example/mirror/envoy:test"
			hostname := name + "." + namespace + ".svc.cluster.local"
			rendered := render(t, "../charts/agent-router", "--namespace", namespace, "--set",
				"ai-gateway-helm.controller.fullnameOverride="+name+
					",platform.agentRouter.envoyProxy.image="+image+"-fleet"+
					",ai-gateway-helm.controller.extraContainers.controller-tls.image="+image+"-controller-tls"+
					",gateway-helm.config.envoyGateway.extensionManager.service.fqdn.hostname="+hostname+
					",gateway-helm.config.envoyGateway.extensionManager.service.tls.certificateRef.name=custom-ca")
			proxies, configs, services, fleets := 0, 0, 0, 0
			if strings.Contains(rendered, "extension-tls") || strings.Contains(rendered, "extension-trust") {
				t.Fatal("native extension TLS must not deploy the origination sidecar or its volumes")
			}
			for _, doc := range chartDocuments(t, rendered) {
				switch doc["kind"] {
				case "EnvoyProxy":
					fleets++
					if chartMap(doc, "spec", "provider", "kubernetes", "envoyDeployment", "container")["image"] != image+"-fleet" {
						t.Fatal("fleet must use its own image override")
					}
				case "Service":
					if chartMap(doc, "metadata")["name"] != name {
						continue
					}
					services++
					want := map[int]int{1063: 1163, 18002: 18003, 9443: 9443, 8080: 8080}
					for _, raw := range chartMap(doc, "spec")["ports"].([]any) {
						port := raw.(map[string]any)
						if target, ok := want[port["port"].(int)]; !ok || port["targetPort"] != target {
							t.Fatalf("unexpected controller Service port: %v", port)
						}
						delete(want, port["port"].(int))
					}
					if len(want) != 0 {
						t.Fatalf("missing controller Service ports: %v", want)
					}
				case "Deployment":
					pod := chartMap(doc, "spec", "template", "spec")
					for _, raw := range pod["containers"].([]any) {
						container := raw.(map[string]any)
						switch container["name"] {
						case "ai-gateway-helm":
							for _, probe := range []string{"startupProbe", "livenessProbe", "readinessProbe"} {
								if chartMap(container, probe, "grpc")["port"] != 1063 {
									t.Fatalf("native gRPC probe changed: %v", container[probe])
								}
							}
						case "controller-tls":
							proxies++
							if container["image"] != image+"-"+container["name"].(string) {
								t.Fatalf("TLS proxy must use its own image override: %v", container["image"])
							}
							wantArgs := []any{"-c", "/etc/podplane-tls/envoy.yaml", "--concurrency", "1", "--log-level", "warning"}
							if !reflect.DeepEqual(container["args"], wantArgs) || len(container["volumeMounts"].([]any)) != 2 || chartMap(container, "securityContext")["runAsNonRoot"] != true {
								t.Fatal("image-only override lost TLS proxy arguments, mounts or security context")
							}
							for _, raw := range container["volumeMounts"].([]any) {
								mount := raw.(map[string]any)
								if mount["readOnly"] != true || mount["subPath"] != nil {
									t.Fatalf("TLS proxy must mount rotating directories read-only: %v", mount)
								}
							}
						}
					}
				case "ConfigMap":
					if chartMap(doc, "metadata")["name"] == "envoy-gateway-config" {
						var config map[string]any
						if err := yaml.Unmarshal([]byte(chartMap(doc, "data")["envoy-gateway.yaml"].(string)), &config); err != nil {
							t.Fatal(err)
						}
						service := chartMap(config, "extensionManager", "service")
						if chartMap(service, "fqdn")["hostname"] != hostname || chartMap(service, "tls", "certificateRef")["name"] != "custom-ca" {
							t.Fatalf("extension TLS overrides lost: %v", service)
						}
					}
					var want []string
					switch chartMap(doc, "metadata")["name"] {
					case "agent-router-controller-tls":
						want = []string{"DownstreamTlsContext", "port_value: 1163", "port_value: 18003", "address: 127.0.0.1, port_value: 1063", "address: 127.0.0.1, port_value: 18002", "watched_directory: {path: /certs}", "filename: /certs/tls.crt", "filename: /certs/tls.key"}
					default:
						continue
					}
					configs++
					text := ""
					for _, value := range chartMap(doc, "data") {
						text += value.(string)
					}
					for _, part := range append(want, "TLSv1_2", "alpn_protocols: [h2]", "path_config_source:") {
						if !strings.Contains(text, part) {
							t.Errorf("TLS configuration missing %q", part)
						}
					}
				}
			}
			if proxies != 1 || configs != 1 || services != 1 || fleets != 1 {
				t.Fatalf("got %d proxies, %d configs, %d Services, %d fleets", proxies, configs, services, fleets)
			}
		})
	}
}
