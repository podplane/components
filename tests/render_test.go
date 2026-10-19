// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/ext"
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

// validatingPolicy is a rendered ValidatingAdmissionPolicy compiled for evaluation.
type validatingPolicy struct {
	matchConditions, validations []cel.Program
}

// renderValidatingPolicy renders chart and compiles the named policy, failing
// the calling test unless the policy fails closed and is bound with Deny.
func renderValidatingPolicy(t *testing.T, chart, name string, args ...string) validatingPolicy {
	t.Helper()
	var spec map[string]any
	bound := false
	for _, doc := range chartDocuments(t, render(t, chart, args...)) {
		switch {
		case doc["kind"] == "ValidatingAdmissionPolicy" && chartMap(doc, "metadata")["name"] == name:
			spec = chartMap(doc, "spec")
		case doc["kind"] == "ValidatingAdmissionPolicyBinding" && chartMap(doc, "spec")["policyName"] == name:
			bound = slices.Contains(chartMap(doc, "spec")["validationActions"].([]any), any("Deny"))
		}
	}
	if spec == nil || !bound || spec["failurePolicy"] != "Fail" {
		t.Fatalf("%s must exist, fail closed, and be bound with Deny", name)
	}
	env, err := cel.NewEnv(
		cel.OptionalTypes(),
		ext.Strings(ext.StringsVersion(2)),
		cel.Variable("object", cel.DynType),
		cel.Variable("oldObject", cel.DynType),
		cel.Variable("namespaceObject", cel.DynType),
		cel.Variable("request", cel.DynType),
	)
	if err != nil {
		t.Fatal(err)
	}
	compile := func(field string) []cel.Program {
		var programs []cel.Program
		for _, raw := range spec[field].([]any) {
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
		return programs
	}
	policy := validatingPolicy{validations: compile("validations")}
	if spec["matchConditions"] != nil {
		policy.matchConditions = compile("matchConditions")
	}
	return policy
}

// admits reports whether the policy admits a request with the given CEL
// variables. Requests that do not meet every match condition are admitted.
func (p validatingPolicy) admits(t *testing.T, vars map[string]any) bool {
	t.Helper()
	eval := func(programs []cel.Program) bool {
		for _, program := range programs {
			out, _, err := program.Eval(vars)
			if err != nil {
				t.Fatal(err)
			}
			if out != types.True {
				return false
			}
		}
		return true
	}
	return !eval(p.matchConditions) || eval(p.validations)
}
