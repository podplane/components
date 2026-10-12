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

// TestSharedMetadata verifies metadata is shared only by compatible components.
func TestSharedMetadata(t *testing.T) {
	got, err := sharedMetadata([]string{"agent-sandbox", "secrets-store-csi-driver"})
	if err != nil {
		t.Fatal(err)
	}
	want := metadata{Addon: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sharedMetadata() = %#v, want %#v", got, want)
	}

	if _, err := sharedMetadata([]string{"agent-sandbox", "cluster-api"}); err == nil {
		t.Fatal("sharedMetadata() returned nil error for differing component metadata")
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
