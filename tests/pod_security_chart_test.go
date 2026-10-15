// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/pod-security-admission/api"
	"k8s.io/pod-security-admission/policy"
)

// controllerPodLevels lists components whose namespace level is set by Pods a
// controller creates at runtime rather than by Pods the chart renders.
var controllerPodLevels = map[string]api.Level{
	// The platform Gateway's Envoy Pods use hostPort 443 and, with node
	// certificates, a hostPath volume.
	"envoy-gateway": api.LevelPrivileged,
}

// TestPodSecurityLevels checks that every app namespace declares the strictest
// Pod Security level its rendered Pods satisfy.
func TestPodSecurityLevels(t *testing.T) {
	data, err := os.ReadFile("../charts/platform-components/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		Platform struct {
			Components struct {
				Apps map[string]struct {
					Namespace   string `yaml:"namespace"`
					PodSecurity string `yaml:"podSecurity"`
				} `yaml:"apps"`
			} `yaml:"components"`
		} `yaml:"platform"`
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		t.Fatal(err)
	}
	evaluator, err := policy.NewEvaluator(policy.DefaultChecks(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, app := range values.Platform.Components.Apps {
		t.Run(name, func(t *testing.T) {
			level := app.PodSecurity
			if app.Namespace == "platform-cluster" {
				// The namespaces template labels the shared namespace restricted.
				level = string(api.LevelRestricted)
			}
			declared, err := api.ParseLevel(level)
			if err != nil {
				t.Fatalf("podSecurity: %v", err)
			}
			args := []string{}
			if _, err := os.Stat("values/" + name + ".yaml"); err == nil {
				args = append(args, "-f", "values/"+name+".yaml")
			}
			want := api.LevelRestricted
			var reasons []string
			for _, doc := range chartDocuments(t, render(t, "../charts/"+name, args...)) {
				template := podTemplate(doc)
				// Flux does not run Helm test hooks.
				if template == nil || chartMap(doc, "metadata", "annotations")["helm.sh/hook"] == "test" {
					continue
				}
				var pod corev1.PodTemplateSpec
				raw, _ := json.Marshal(template)
				if err := json.Unmarshal(raw, &pod); err != nil {
					t.Fatal(err)
				}
				for _, level := range []api.Level{api.LevelRestricted, api.LevelBaseline} {
					var failed []string
					for _, result := range evaluator.EvaluatePod(api.LevelVersion{Level: level, Version: api.LatestVersion()}, &pod.ObjectMeta, &pod.Spec) {
						if !result.Allowed {
							failed = append(failed, result.ForbiddenReason+" ("+result.ForbiddenDetail+")")
						}
					}
					if len(failed) == 0 {
						break
					}
					if level == api.LevelBaseline {
						want = api.LevelPrivileged
					} else if want == api.LevelRestricted {
						want = api.LevelBaseline
					}
					reasons = append(reasons, chartMap(doc, "metadata")["name"].(string)+" violates "+string(level)+": "+strings.Join(failed, "; "))
				}
			}
			if level, ok := controllerPodLevels[name]; ok {
				want = level
			}
			if declared != want {
				t.Errorf("podSecurity is %q, want %q\n%s", declared, want, strings.Join(reasons, "\n"))
			}
		})
	}
}

// podTemplate returns the Pod template of a rendered workload, or nil.
func podTemplate(doc map[string]any) map[string]any {
	switch doc["kind"] {
	case "Pod":
		return doc
	case "Deployment", "DaemonSet", "StatefulSet", "ReplicaSet", "Job":
		return chartMap(doc, "spec", "template")
	case "CronJob":
		return chartMap(doc, "spec", "jobTemplate", "spec", "template")
	}
	return nil
}
