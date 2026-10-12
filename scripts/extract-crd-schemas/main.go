// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

// Extract CRD schemas from rendered YAML for kubeconform.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// main reads rendered YAML from stdin and writes schemas to the output directory.
func main() {
	output := flag.String("output", "", "Directory for custom-resource schemas")
	flag.Parse()
	if err := run(*output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run extracts each CRD version's schema using kubeconform's lookup filenames.
func run(output string) error {
	if output == "" {
		return fmt.Errorf("--output is required")
	}
	decoder := yaml.NewDecoder(os.Stdin)
	for {
		var doc map[string]any
		if err := decoder.Decode(&doc); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if doc["kind"] != "CustomResourceDefinition" {
			continue
		}
		spec := doc["spec"].(map[string]any)
		kind := strings.ToLower(spec["names"].(map[string]any)["kind"].(string))
		for _, raw := range spec["versions"].([]any) {
			v := raw.(map[string]any)
			schema := v["schema"].(map[string]any)["openAPIV3Schema"].(map[string]any)
			strictSchema(schema)
			path := filepath.Join(output, spec["group"].(string), kind+"_"+v["name"].(string)+".json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			body, err := json.Marshal(schema)
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, body, 0o600); err != nil {
				return err
			}
		}
	}
}

// strictSchema rejects unknown structured fields, preserves maps and opaque
// fields, and translates OpenAPI nullable into JSON Schema types.
func strictSchema(value any) {
	switch value := value.(type) {
	case map[string]any:
		if value["type"] == "object" && value["properties"] != nil && value["additionalProperties"] == nil && value["x-kubernetes-preserve-unknown-fields"] != true {
			value["additionalProperties"] = false
		}
		if value["nullable"] == true {
			if kind, ok := value["type"].(string); ok {
				value["type"] = []string{kind, "null"}
			}
		}
		for _, child := range value {
			strictSchema(child)
		}
	case []any:
		for _, child := range value {
			strictSchema(child)
		}
	}
}
