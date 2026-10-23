// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestValidateImageRef verifies conflicting references to one image are rejected.
func TestValidateImageRef(t *testing.T) {
	tests := []struct {
		name    string
		refs    []string
		wantErr bool
	}{
		{name: "same reference", refs: []string{"registry.k8s.io/pause:3.10.2", "registry.k8s.io/pause:3.10.2"}},
		{name: "implicit latest", refs: []string{"busybox", "docker.io/library/busybox:latest"}},
		{name: "docker hub alias conflict", refs: []string{"docker.io/library/busybox:1", "index.docker.io/busybox:2"}, wantErr: true},
		{name: "docker hub shorthand conflict", refs: []string{"busybox:1", "docker.io/busybox:2"}, wantErr: true},
		{name: "different tags", refs: []string{"registry.k8s.io/pause:3.10.1", "registry.k8s.io/pause:3.10.2"}, wantErr: true},
		{name: "different digests", refs: []string{"example.com/app@sha256:one", "example.com/app@sha256:two"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen := map[string]string{}
			var err error
			for _, ref := range tt.refs {
				if err = validateImageRef(seen, ref); err != nil {
					break
				}
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateImageRef() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestNormalizeImage verifies image references are converted to canonical form.
func TestNormalizeImage(t *testing.T) {
	tests := map[string]string{
		"busybox":                         "docker.io/library/busybox:latest",
		"busybox:1":                       "docker.io/library/busybox:1",
		"docker.io/busybox":               "docker.io/library/busybox:latest",
		"index.docker.io/library/busybox": "docker.io/library/busybox:latest",
		"example.com/team/app":            "example.com/team/app:latest",
		"localhost:5000/app:v1":           "localhost:5000/app:v1",
	}
	for input, want := range tests {
		if got := normalizeImage(input); got != want {
			t.Errorf("normalizeImage(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestSharedMetadata verifies shared images cover all their consumers.
func TestSharedMetadata(t *testing.T) {
	got := sharedMetadata([]string{"agent-sandbox", "secrets-store-csi-driver"})
	want := metadata{Addon: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sharedMetadata() = %#v, want %#v", got, want)
	}

	for _, tc := range []struct {
		components []string
		want       metadata
	}{
		{[]string{"agent-sandbox", "cluster-api"}, metadata{}},
		{[]string{"csi-gce-pd", "csi-openebs-lvm"}, metadata{Providers: []string{"google", "local"}}},
		{[]string{"csi-openebs-lvm", "csi-gce-pd", "csi-openebs-lvm-crds"}, metadata{Providers: []string{"google", "local"}}},
		{[]string{"csi-gce-pd", "secrets-store-csi-driver"}, metadata{}},
	} {
		got := sharedMetadata(tc.components)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("sharedMetadata(%v) = %#v; want %#v", tc.components, got, tc.want)
		}
	}
}

// Check the public offline command against real chart renders, including stale
// architecture coverage and provider selection that valid JSON cannot catch.
func TestManifestCheckCLI(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is required")
	}
	binary := filepath.Join(t.TempDir(), "manifests")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	body, err := os.ReadFile("../../manifests/components.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, message string
		mutate        func(*manifest)
	}{
		{name: "complete"},
		{name: "missing architecture", message: "requires both", mutate: func(doc *manifest) { doc.Components.Images = doc.Components.Images[1:] }},
		{name: "duplicate architecture", message: "duplicate architecture", mutate: func(doc *manifest) { doc.Components.Images = append(doc.Components.Images, doc.Components.Images[0]) }},
		{name: "invalid digest", message: "invalid source, digest", mutate: func(doc *manifest) { doc.Components.Images[0].Digest = "latest" }},
		{name: "stale membership", message: "stale component membership", mutate: func(doc *manifest) { doc.Components.Images[0].Components = []string{"wrong"} }},
		{name: "wrong provider", message: "provider/addon metadata", mutate: func(doc *manifest) {
			for i := range doc.Components.Images {
				if strings.Contains(doc.Components.Images[i].Image, "nstance-operator") {
					doc.Components.Images[i].Providers = []string{"aws"}
				}
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var doc manifest
			if err := json.Unmarshal(body, &doc); err != nil {
				t.Fatal(err)
			}
			if test.mutate != nil {
				test.mutate(&doc)
			}
			input, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "components.json")
			if err := os.WriteFile(path, input, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "--check", "--output", path)
			cmd.Dir = "../.."
			output, err := cmd.CombinedOutput()
			if (err != nil) != (test.message != "") || !strings.Contains(string(output), test.message) {
				t.Fatalf("check: %v: %s; want %q", err, output, test.message)
			}
			if after, err := os.ReadFile(path); err != nil || string(after) != string(input) {
				t.Fatalf("check modified the inventory: %v", err)
			}
		})
	}
}

// TestComponentUpdateCLI exercises a scoped update with real Helm renders and
// an offline registry fixture, including shared and removed image memberships.
func TestComponentUpdateCLI(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is required")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "manifests")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"envoy-gateway", "podplane-operator", "envoy-gateway-crds"} {
		write("charts/"+name+"/Chart.yaml", "apiVersion: v2\nname: "+name+"\nversion: 0.1.0\n", 0o600)
	}
	write("charts/podplane-operator/templates/broken.yaml", "{{ fail \"unrelated chart must not render\" }}", 0o600)
	write("charts/envoy-gateway/templates/pod.yaml", `apiVersion: v1
kind: Pod
metadata:
  name: example
spec:
  containers:
    - name: new
      image: example.com/new:v2
    - name: shared
      image: example.com/shared:v1
`, 0o600)
	write("bin/crane", `#!/bin/sh
set -eu
case "$2" in
  example.com/new:*|example.com/new@*|example.com/shared:*|example.com/shared@*|docker.io/envoyproxy/envoy:*|docker.io/envoyproxy/envoy@*) ;;
  *) echo "unrelated image must not resolve: $2" >&2; exit 1 ;;
esac
case "$1" in
  digest) echo 'sha256:index' ;;
  manifest)
    case "$2" in
      *@sha256:index) echo '{"manifests":[{"digest":"sha256:amd64","platform":{"os":"linux","architecture":"amd64"}},{"digest":"sha256:arm64","platform":{"os":"linux","architecture":"arm64"}}]}' ;;
      *) echo '{"config":{"size":10},"layers":[{"size":20}]}' ;;
    esac ;;
  *) exit 1 ;;
esac
`, 0o700)
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VERSION", "must-not-change-the-manifest-version")
	initial := manifest{Components: components{
		Version: "1.2.3",
		Source:  source{URL: "https://example.com/custom.git", Ref: sourceRef{Commit: "abc"}},
		Images: []image{
			{Components: []string{"envoy-gateway"}, Image: "example.com/old:v1", Digest: "old", Size: 10, Addon: true},
			{Components: []string{"envoy-gateway", "podplane-operator"}, Image: "example.com/shared:v1", Digest: "old", Size: 10, Addon: true},
			{Components: []string{"envoy-gateway", "podplane-operator"}, Image: "example.com/removed-shared:v1", Digest: "keep", Size: 20, Addon: true, Cached: true},
			{Components: []string{"podplane-operator"}, Image: "example.com/unrelated:v1", Digest: "keep", Size: 30, Addon: true, Cached: true},
			{Image: "ghcr.io/podplane/hello:latest", Digest: "keep", Size: 40, Cached: true},
			{Components: []string{"envoy-gateway"}, Image: "registry.k8s.io/pause:3.10.2", Digest: "keep", Size: 50, Addon: true},
		},
	}}
	body, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	write("components.json", string(body), 0o600)
	cmd := exec.Command(binary, "--component", "envoy-gateway", "--output", "components.json")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update: %v: %s", err, output)
	}
	after, err := os.ReadFile(filepath.Join(root, "components.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got manifest
	if err := json.Unmarshal(after, &got); err != nil {
		t.Fatal(err)
	}
	want := initial
	want.Components.Images = slices.Clone(initial.Components.Images[2:])
	want.Components.Images[0].Components = []string{"podplane-operator"}
	want.Components.Images[3].Components = nil
	want.Components.Images[3].Addon = false
	for _, ref := range []string{"example.com/new:v2", "example.com/shared:v1", "docker.io/envoyproxy/envoy:distroless-v1.39.1"} {
		members := []string{"envoy-gateway"}
		switch ref {
		case "example.com/shared:v1":
			members = append(members, "podplane-operator")
		case "docker.io/envoyproxy/envoy:distroless-v1.39.1":
			// The extra Envoy image is shared with Agent Router's proxy fleet.
			members = []string{"agent-router", "envoy-gateway"}
		}
		for _, arch := range []string{"amd64", "arm64"} {
			want.Components.Images = append(want.Components.Images, image{
				Components: members, Image: ref, Digest: "sha256:" + arch,
				Size: 46 + 10 + 20, Platform: "linux/" + arch, Index: "sha256:index", Addon: true,
			})
		}
	}
	slices.SortFunc(want.Components.Images, func(a, b image) int {
		if n := strings.Compare(a.Image, b.Image); n != 0 {
			return n
		}
		return strings.Compare(a.Platform, b.Platform)
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scoped manifest = %#v; want %#v", got, want)
	}
	for _, test := range []struct {
		name, component, message string
		args                     []string
	}{
		{name: "unknown", component: "missing", message: "unknown non-CRD component"},
		{name: "CRD", component: "envoy-gateway-crds", message: "unknown non-CRD component"},
		{name: "check", component: "envoy-gateway", args: []string{"--check"}, message: "cannot be combined"},
		{name: "missing manifest", component: "envoy-gateway", args: []string{"--output", "missing.json"}, message: "read existing manifest"},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"--component", test.component, "--output", "components.json"}, test.args...)
			cmd := exec.Command(binary, args...)
			cmd.Dir = root
			if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), test.message) {
				t.Fatalf("update: %v: %s; want %q", err, output, test.message)
			}
			if unchanged, err := os.ReadFile(filepath.Join(root, "components.json")); err != nil || string(unchanged) != string(after) {
				t.Fatalf("failed update modified manifest: %v", err)
			}
		})
	}
}
