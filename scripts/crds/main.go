// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0
//
// Updates and checks Podplane-managed CRDs.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	oldDir      = flag.String("old", "", "Directory containing previous CRD YAML files.")
	newDir      = flag.String("new", "", "Directory containing updated CRD YAML files.")
	updateChart = flag.String("update", "", "CRD chart to update, or 'all'.")
)

type crdChart struct {
	Name   string
	Env    string
	Update func(ctx updateContext) error
}

type updateContext struct {
	OutDir  string
	Version string
}

type crdInfo struct {
	Name           string
	File           string
	ServedVersions map[string]bool
}

var charts = []crdChart{
	{
		Name: "agent-sandbox-crds",
		Env:  "AGENT_SANDBOX_VERSION",
		Update: func(ctx updateContext) error {
			version := ctx.Version
			if version == "" {
				latest, err := githubLatestTag("kubernetes-sigs/agent-sandbox")
				if err != nil {
					return err
				}
				version = latest
			}
			files, err := githubYAMLFiles("kubernetes-sigs/agent-sandbox", "helm/crds", version)
			if err != nil {
				return err
			}
			baseURL := fmt.Sprintf("https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/refs/tags/%s/helm/crds", version)
			for _, file := range files {
				if err := downloadFile(baseURL+"/"+file, filepath.Join(ctx.OutDir, file)); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Name: "cert-manager-crds",
		Env:  "CERT_MANAGER_VERSION",
		Update: func(ctx updateContext) error {
			version := ctx.Version
			if version == "" {
				latest, err := githubLatestTag("cert-manager/cert-manager")
				if err != nil {
					return err
				}
				version = latest
			}
			url := fmt.Sprintf("https://github.com/cert-manager/cert-manager/releases/download/%s/cert-manager.crds.yaml", version)
			if err := downloadAndSplit(url, ctx.OutDir); err != nil {
				return err
			}

			approverVersion := firstNonEmpty(os.Getenv("CERT_MANAGER_APPROVER_POLICY_VERSION"), os.Getenv("APPROVER_POLICY_VERSION"))
			if approverVersion == "" {
				latest, err := githubLatestTag("cert-manager/approver-policy")
				if err != nil {
					return err
				}
				approverVersion = latest
			}
			return renderHelmCRDs(ctx.OutDir, helmDependency{
				Name:       "cert-manager-approver-policy",
				Release:    "cert-manager-crds",
				Version:    approverVersion,
				Repository: "https://charts.jetstack.io",
			}, map[string]any{})
		},
	},
	{
		Name: "cilium-crds",
		Env:  "CILIUM_VERSION",
		Update: func(ctx updateContext) error {
			version := ctx.Version
			if version == "" {
				latest, err := githubLatestTag("cilium/cilium")
				if err != nil {
					return err
				}
				version = latest
			}
			for _, apiVersion := range []string{"v2", "v2alpha1"} {
				path := "pkg/k8s/apis/cilium.io/client/crds/" + apiVersion
				files, err := githubYAMLFiles("cilium/cilium", path, version)
				if err != nil {
					return err
				}
				baseURL := fmt.Sprintf("https://raw.githubusercontent.com/cilium/cilium/refs/tags/%s/%s", version, path)
				for _, file := range files {
					if err := downloadFile(baseURL+"/"+file, filepath.Join(ctx.OutDir, file)); err != nil {
						return err
					}
				}
			}
			return nil
		},
	},
	{
		Name: "fluxcd-crds",
		Env:  "FLUXCD_CHART_VERSION",
		Update: func(ctx updateContext) error {
			version := firstNonEmpty(ctx.Version, os.Getenv("CHART_VERSION"), "2.18.3")
			return renderHelmCRDs(ctx.OutDir, helmDependency{
				Name:       "flux2",
				Release:    "fluxcd-crds",
				Version:    version,
				Repository: firstNonEmpty(os.Getenv("CHART_REPO"), "https://fluxcd-community.github.io/helm-charts"),
			}, map[string]any{
				"flux2": map[string]any{
					"installCRDs":               true,
					"helmController":            map[string]any{"create": true},
					"sourceController":          map[string]any{"create": true},
					"kustomizeController":       map[string]any{"create": true},
					"notificationController":    map[string]any{"create": true},
					"imageAutomationController": map[string]any{"create": true},
					"imageReflectionController": map[string]any{"create": true},
				},
			})
		},
	},
	{
		Name: "gateway-api-crds",
		Env:  "GATEWAY_API_VERSION",
		Update: func(ctx updateContext) error {
			version := ctx.Version
			if version == "" {
				latest, err := githubLatestTag("kubernetes-sigs/gateway-api")
				if err != nil {
					return err
				}
				version = latest
			}
			url := fmt.Sprintf("https://github.com/kubernetes-sigs/gateway-api/releases/download/%s/experimental-install.yaml", version)
			return downloadAndSplit(url, ctx.OutDir)
		},
	},
	{
		Name: "snapshot-crds",
		Env:  "SNAPSHOT_VERSION",
		Update: func(ctx updateContext) error {
			version := ctx.Version
			if version == "" {
				latest, err := githubLatestTag("kubernetes-csi/external-snapshotter")
				if err != nil {
					return err
				}
				version = latest
			}
			files := []string{
				"groupsnapshot.storage.k8s.io_volumegroupsnapshotclasses.yaml",
				"groupsnapshot.storage.k8s.io_volumegroupsnapshotcontents.yaml",
				"groupsnapshot.storage.k8s.io_volumegroupsnapshots.yaml",
				"snapshot.storage.k8s.io_volumesnapshotclasses.yaml",
				"snapshot.storage.k8s.io_volumesnapshotcontents.yaml",
				"snapshot.storage.k8s.io_volumesnapshots.yaml",
			}
			baseURL := fmt.Sprintf("https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/refs/tags/%s/client/config/crd", version)
			for _, file := range files {
				if err := downloadFile(baseURL+"/"+file, filepath.Join(ctx.OutDir, file)); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Name: "traefik-crds",
		Env:  "TRAEFIK_CHART_VERSION",
		Update: func(ctx updateContext) error {
			version := ctx.Version
			if version == "" {
				latest, err := githubLatestTag("traefik/traefik-helm-chart")
				if err != nil {
					return err
				}
				version = latest
			}
			return renderHelmCRDs(ctx.OutDir, helmDependency{
				Name:       "traefik",
				Release:    "traefik-crds",
				Version:    version,
				Repository: "https://traefik.github.io/charts",
			}, map[string]any{})
		},
	},
	{
		Name: "trust-manager-crds",
		Env:  "TRUST_MANAGER_VERSION",
		Update: func(ctx updateContext) error {
			version := ctx.Version
			if version == "" {
				latest, err := githubLatestTag("cert-manager/trust-manager")
				if err != nil {
					return err
				}
				version = latest
			}
			return renderHelmCRDs(ctx.OutDir, helmDependency{
				Name:       "trust-manager",
				Release:    "trust-manager-crds",
				Version:    version,
				Repository: "https://charts.jetstack.io",
			}, map[string]any{})
		},
	},
}

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if *updateChart != "" {
		return updateCRDs(*updateChart)
	}
	if *oldDir == "" || *newDir == "" {
		return fmt.Errorf("usage: go run ./scripts/crds --old <previous-crds-dir> --new <updated-crds-dir> OR go run ./scripts/crds --update <chart|all>")
	}
	return checkCompatibility(*oldDir, *newDir)
}

func updateCRDs(name string) error {
	repoRoot, err := os.Getwd()
	if err != nil {
		return err
	}

	selected := charts
	if name != "all" {
		selected = nil
		for _, chart := range charts {
			if chart.Name == name {
				selected = []crdChart{chart}
				break
			}
		}
		if selected == nil {
			return fmt.Errorf("unknown CRD chart %q", name)
		}
	}

	for _, chart := range selected {
		fmt.Printf("==> Updating %s\n", chart.Name)
		if err := updateOne(repoRoot, chart); err != nil {
			return err
		}
	}
	return nil
}

func updateOne(repoRoot string, chart crdChart) error {
	targetDir := filepath.Join(repoRoot, "charts", chart.Name, "templates", "external")
	work, err := os.MkdirTemp("", "podplane-crds-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	oldDir := filepath.Join(work, "old")
	newDir := filepath.Join(work, "new")
	if err := copyDir(targetDir, oldDir); err != nil {
		return err
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return err
	}

	version := firstNonEmpty(os.Getenv(chart.Env), os.Getenv("DL_VERSION"))
	if version != "" {
		fmt.Printf("Using %s=%s\n", chart.Env, version)
	}
	if err := chart.Update(updateContext{OutDir: newDir, Version: version}); err != nil {
		return err
	}
	if err := preserveFilenames(oldDir, newDir); err != nil {
		return err
	}
	if err := checkCompatibility(oldDir, newDir); err != nil {
		return err
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return err
	}
	if err := copyDir(newDir, targetDir); err != nil {
		return err
	}
	fmt.Printf("Updated %s\n", chart.Name)
	return nil
}

func checkCompatibility(oldPath, newPath string) error {
	oldCRDs, err := readCRDs(oldPath)
	if err != nil {
		return fmt.Errorf("read previous CRDs: %w", err)
	}
	newCRDs, err := readCRDs(newPath)
	if err != nil {
		return fmt.Errorf("read updated CRDs: %w", err)
	}

	errors := []string{}
	for name, old := range oldCRDs {
		updated, ok := newCRDs[name]
		if !ok {
			errors = append(errors, fmt.Sprintf("CRD %s was removed", name))
			continue
		}
		for version := range old.ServedVersions {
			if !updated.ServedVersions[version] {
				errors = append(errors, fmt.Sprintf("CRD %s no longer serves version %s", name, version))
			}
		}
	}

	if len(errors) > 0 {
		sort.Strings(errors)
		return fmt.Errorf("breaking CRD update detected:\n  %s", strings.Join(errors, "\n  "))
	}

	fmt.Printf("CRD compatibility check passed (%d existing CRD(s), %d updated CRD(s)).\n", len(oldCRDs), len(newCRDs))
	return nil
}

func preserveFilenames(oldPath, newPath string) error {
	oldCRDs, err := readCRDs(oldPath)
	if err != nil {
		return err
	}
	newCRDs, err := readCRDs(newPath)
	if err != nil {
		return err
	}
	for name, old := range oldCRDs {
		updated, ok := newCRDs[name]
		if !ok || old.File == "" || updated.File == "" || old.File == updated.File {
			continue
		}
		from := filepath.Join(newPath, updated.File)
		to := filepath.Join(newPath, old.File)
		if _, err := os.Stat(to); err == nil {
			continue
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	return nil
}

type helmDependency struct {
	Name       string
	Release    string
	Version    string
	Repository string
}

func renderHelmCRDs(outDir string, dep helmDependency, values map[string]any) error {
	release := firstNonEmpty(dep.Release, dep.Name)
	if len(values) == 0 {
		rendered, err := commandOutput("helm", "template", release, dep.Name, "--repo", dep.Repository, "--version", dep.Version, "--include-crds")
		if err != nil {
			return err
		}
		return splitCRDs([]byte(rendered), outDir)
	}

	work, err := os.MkdirTemp("", "podplane-helm-crds-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	chartYAML := fmt.Sprintf("apiVersion: v2\nname: %s-render\nversion: 0.0.0\ndependencies:\n  - name: %s\n    version: %q\n    repository: %q\n", release, dep.Name, dep.Version, dep.Repository)
	if err := os.WriteFile(filepath.Join(work, "Chart.yaml"), []byte(chartYAML), 0o644); err != nil {
		return err
	}
	valuesYAML, err := yaml.Marshal(values)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "values.yaml"), valuesYAML, 0o644); err != nil {
		return err
	}

	if _, err := commandOutput("helm", "dependency", "update", work); err != nil {
		return err
	}
	rendered, err := commandOutput("helm", "template", release, work, "--include-crds")
	if err != nil {
		return err
	}
	return splitCRDs([]byte(rendered), outDir)
}

func downloadAndSplit(url, outDir string) error {
	body, err := download(url)
	if err != nil {
		return err
	}
	return splitCRDs(body, outDir)
}

func splitCRDs(body []byte, outDir string) error {
	files := map[string][][]byte{}
	for _, rawDoc := range splitYAMLDocuments(body) {
		var doc map[string]any
		if err := yaml.Unmarshal(rawDoc, &doc); err != nil {
			return err
		}
		if doc == nil || doc["kind"] != "CustomResourceDefinition" {
			continue
		}
		info, err := parseCRD(doc, "rendered CRD")
		if err != nil {
			return err
		}
		fileName := firstNonEmpty(sourceBasename(rawDoc), info.Name+".yaml")
		rawDoc = bytes.TrimSpace(rawDoc)
		if !bytes.HasPrefix(rawDoc, []byte("---\n")) {
			rawDoc = append([]byte("---\n"), rawDoc...)
		}
		files[fileName] = append(files[fileName], append(rawDoc, '\n'))
	}
	for fileName, docs := range files {
		path := filepath.Join(outDir, fileName)
		if err := os.WriteFile(path, bytes.Join(docs, nil), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func splitYAMLDocuments(body []byte) [][]byte {
	lines := bytes.Split(body, []byte("\n"))
	docs := [][]byte{}
	current := [][]byte{}
	for _, line := range lines {
		if bytes.Equal(bytes.TrimSpace(line), []byte("---")) {
			if len(bytes.TrimSpace(bytes.Join(current, []byte("\n")))) > 0 {
				docs = append(docs, bytes.Join(current, []byte("\n")))
			}
			current = [][]byte{}
			continue
		}
		current = append(current, line)
	}
	if len(bytes.TrimSpace(bytes.Join(current, []byte("\n")))) > 0 {
		docs = append(docs, bytes.Join(current, []byte("\n")))
	}
	return docs
}

func sourceBasename(doc []byte) string {
	for _, line := range bytes.Split(doc, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("# Source: ")) {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(string(line), "# Source: "))
		base := filepath.Base(path)
		if strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml") {
			return base
		}
	}
	return ""
}

func githubLatestTag(repo string) (string, error) {
	body, err := download("https://api.github.com/repos/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	var response struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}
	if response.TagName == "" {
		return "", fmt.Errorf("GitHub latest release for %s did not include tag_name", repo)
	}
	return response.TagName, nil
}

func githubYAMLFiles(repo, path, ref string) ([]string, error) {
	body, err := download(fmt.Sprintf("https://api.github.com/repos/%s/contents/%s?ref=%s", repo, path, ref))
	if err != nil {
		return nil, err
	}
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}
	files := []string{}
	for _, entry := range entries {
		if entry.Type == "file" && strings.HasSuffix(entry.Name, ".yaml") {
			files = append(files, entry.Name)
		}
	}
	sort.Strings(files)
	return files, nil
}

func downloadFile(url, path string) error {
	body, err := download(url)
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func download(url string) ([]byte, error) {
	fmt.Printf("GET %s\n", url)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func readCRDs(dir string) (map[string]crdInfo, error) {
	crds := map[string]crdInfo{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			return nil
		}
		items, err := readCRDFile(path)
		if err != nil {
			return err
		}
		for _, item := range items {
			item.File = filepath.Base(path)
			crds[item.Name] = item
		}
		return nil
	})
	return crds, err
}

func readCRDFile(path string) ([]crdInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	items := []crdInfo{}
	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if doc == nil || doc["kind"] != "CustomResourceDefinition" {
			continue
		}
		item, err := parseCRD(doc, path)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func parseCRD(doc map[string]any, path string) (crdInfo, error) {
	metadata, ok := doc["metadata"].(map[string]any)
	if !ok {
		return crdInfo{}, fmt.Errorf("CRD in %s has no metadata", path)
	}
	name, ok := metadata["name"].(string)
	if !ok || name == "" {
		return crdInfo{}, fmt.Errorf("CRD in %s has no metadata.name", path)
	}

	spec, ok := doc["spec"].(map[string]any)
	if !ok {
		return crdInfo{}, fmt.Errorf("CRD %s in %s has no spec", name, path)
	}
	versions, ok := spec["versions"].([]any)
	if !ok {
		return crdInfo{}, fmt.Errorf("CRD %s in %s has no spec.versions", name, path)
	}

	servedVersions := map[string]bool{}
	for _, value := range versions {
		version, ok := value.(map[string]any)
		if !ok {
			continue
		}
		served, _ := version["served"].(bool)
		versionName, _ := version["name"].(string)
		if served && versionName != "" {
			servedVersions[versionName] = true
		}
	}
	if len(servedVersions) == 0 {
		return crdInfo{}, fmt.Errorf("CRD %s in %s serves no versions", name, path)
	}

	return crdInfo{Name: name, ServedVersions: servedVersions}, nil
}

func copyDir(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		out := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(out, body, 0o644)
	})
}

func commandOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
