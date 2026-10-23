// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tests

import (
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// TestCoreDNSStaticRecords checks empty defaults, dual-stack records and the
// mounted hosts file while preserving service discovery and upstream forwarding.
func TestCoreDNSStaticRecords(t *testing.T) {
	for _, records := range []map[string][]string{
		{},
		{"host.podplane.internal": {"10.0.2.2"}, "dual.example": {"192.0.2.13", "2001:db8::19"}},
	} {
		values, err := json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		docs := chartDocuments(t, render(t, "../charts/coredns", "--namespace", "platform-coredns", "--set-json", "platform.coredns.staticRecords="+string(values)))
		var corefile, hosts, configName string
		var pod map[string]any
		for _, doc := range docs {
			switch doc["kind"] {
			case "ConfigMap":
				data := chartMap(doc, "data")
				if value, ok := data["Corefile"].(string); ok {
					corefile = value
				}
				if value, ok := data["hosts"].(string); ok {
					hosts, configName = value, chartMap(doc, "metadata")["name"].(string)
				}
			case "Deployment":
				pod = chartMap(doc, "spec", "template", "spec")
			}
		}
		got := map[string][]string{}
		for _, line := range strings.Split(strings.TrimSpace(hosts), "\n") {
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 2 {
				t.Fatalf("invalid hosts entry %q", line)
			}
			got[fields[1]] = append(got[fields[1]], fields[0])
		}
		if !reflect.DeepEqual(got, records) {
			t.Fatalf("hosts records = %v, want %v", got, records)
		}
		for _, want := range []string{"hosts /etc/coredns-static/hosts {", "fallthrough\n", "kubernetes cluster.local in-addr.arpa ip6.arpa", "forward . /etc/resolv.conf"} {
			if !strings.Contains(corefile, want) {
				t.Errorf("Corefile is missing %q", want)
			}
		}
		if configName == "" || pod == nil {
			t.Fatal("missing static records ConfigMap or CoreDNS Pod")
		}
		volumeName := ""
		for _, raw := range pod["volumes"].([]any) {
			volume := raw.(map[string]any)
			if chartMap(volume, "configMap")["name"] == configName {
				volumeName = volume["name"].(string)
			}
		}
		mounted := false
		container := pod["containers"].([]any)[0].(map[string]any)
		for _, raw := range container["volumeMounts"].([]any) {
			mount := raw.(map[string]any)
			if volumeName != "" && mount["name"] == volumeName && mount["mountPath"] == "/etc/coredns-static" && mount["readOnly"] == true && mount["subPath"] == nil {
				mounted = true
			}
		}
		if !mounted {
			t.Fatal("static hosts file must be mounted read-only without subPath so updates propagate")
		}
	}
}

// TestCoreDNSRejectsInvalidStaticRecords prevents malformed hosts-file entries.
func TestCoreDNSRejectsInvalidStaticRecords(t *testing.T) {
	for _, records := range []string{
		`{"bad name":["192.0.2.1"]}`,
		`{"host.example":["not-an-ip"]}`,
		`{"host.example":["999.0.0.1"]}`,
		`{"host.example":[]}`,
		`{"host.example":"192.0.2.1"}`,
	} {
		out, err := exec.Command("helm", "template", "test", "../charts/coredns", "--set-json", "platform.coredns.staticRecords="+records).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "schema") {
			t.Fatalf("expected staticRecords validation failure for %s: %v\n%s", records, err, out)
		}
	}
}
