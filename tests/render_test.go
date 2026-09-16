// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"os/exec"
	"strings"
	"testing"
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
