// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestValidateImageRef(t *testing.T) {
	tests := []struct {
		name    string
		refs    []string
		wantErr bool
	}{
		{name: "same reference", refs: []string{"registry.k8s.io/pause:3.10.2", "registry.k8s.io/pause:3.10.2"}},
		{name: "implicit latest", refs: []string{"busybox", "docker.io/library/busybox:latest"}},
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
