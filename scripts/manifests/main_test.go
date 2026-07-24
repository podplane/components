// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"reflect"
	"testing"
)

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

func TestSharedMetadata(t *testing.T) {
	got, err := sharedMetadata([]string{"cert-manager", "secrets-store-csi-driver"})
	if err != nil {
		t.Fatal(err)
	}
	want := metadata{Addon: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sharedMetadata() = %#v, want %#v", got, want)
	}

	if _, err := sharedMetadata([]string{"cert-manager", "cluster-api"}); err == nil {
		t.Fatal("sharedMetadata() returned nil error for differing component metadata")
	}
}
