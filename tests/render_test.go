// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"io"
	"os/exec"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// render renders a chart for assertions and fails the calling test on error.
func render(t *testing.T, chart string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"template", "test", chart, "--kube-version", "1.37.0"}, args...)
	out, err := exec.Command("helm", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm %s failed: %v\n%s", strings.Join(commandArgs, " "), err, out)
	}
	return string(out)
}

// chartDocuments decodes non-empty rendered YAML documents and fails the calling
// test on a decoding error.
func chartDocuments(t *testing.T, rendered string) []map[string]any {
	t.Helper()
	decoder := yaml.NewDecoder(strings.NewReader(rendered))
	var docs []map[string]any
	for {
		var doc map[string]any
		if err := decoder.Decode(&doc); err == io.EOF {
			return docs
		} else if err != nil {
			t.Fatalf("decode chart: %v", err)
		}
		if doc != nil {
			docs = append(docs, doc)
		}
	}
}

// chartMap returns the nested map at path, or nil if any key is missing or not a map.
func chartMap(value map[string]any, path ...string) map[string]any {
	for _, key := range path {
		value, _ = value[key].(map[string]any)
	}
	return value
}
