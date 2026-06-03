// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0
//
// Generates the component source manifest at
// components/manifests/components.json.
//
// The manifest is produced from rendered non-CRD charts plus a small static list
// for images used by conditional templates and hook jobs. It records source
// images only: image is the normalized image reference rendered by the chart,
// and digest is the expected digest.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	outputPath  = flag.String("output", "manifests/components.json", "Path to write the component manifest JSON.")
	version     = flag.String("version", "", "Component manifest version. Defaults to VERSION, or dev when VERSION is unset.")
	extraImages = []extraImage{
		{Repo: "docker.io/library/caddy", Tag: "2"},
		{Repo: "docker.io/library/golang", Tag: "alpine"},
		{Repo: "ghcr.io/podplane/hello", Tag: "latest"},
	}
	supportedPlatforms = map[string]bool{
		"linux/amd64": true,
		"linux/arm64": true,
	}
	componentMetadata = map[string]metadata{
		"agent-sandbox":  {Addon: true},
		"cert-manager":   {Addon: true},
		"platform-certs": {Addon: true},
		"trust-manager":  {Addon: true},
		"platform-trust": {Addon: true},
		"traefik":        {Addon: true},
		"snapshot":       {Addon: true},
		"csi-aws-ebs":    {Providers: []string{"aws"}},
	}
)

type extraImage struct {
	Repo string
	Tag  string
}

type metadata struct {
	Providers []string
	Addon     bool
}

type manifest struct {
	Components components `json:"components"`
}

type components struct {
	Version string  `json:"version"`
	Images  []image `json:"images"`
}

type image struct {
	Component string   `json:"component,omitempty"`
	Image     string   `json:"image"`
	Digest    string   `json:"digest"`
	Size      int64    `json:"size"`
	Platform  string   `json:"platform,omitempty"`
	Index     string   `json:"index,omitempty"`
	Providers []string `json:"providers,omitempty"`
	Addon     bool     `json:"addon,omitempty"`
	Cached    bool     `json:"cached,omitempty"`
}

// main parses flags and exits non-zero when manifest generation fails.
func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run renders non-CRD charts, resolves image metadata, and writes the manifest.
func run() error {
	repoRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	chartsDir := filepath.Join(repoRoot, "charts")
	entries, err := os.ReadDir(chartsDir)
	if err != nil {
		return fmt.Errorf("read charts dir: %w", err)
	}
	chartNames := []string{}
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasSuffix(entry.Name(), "-crds") {
			chartNames = append(chartNames, entry.Name())
		}
	}

	images := []image{}
	sort.Slice(extraImages, func(i, j int) bool {
		if extraImages[i].Repo != extraImages[j].Repo {
			return extraImages[i].Repo < extraImages[j].Repo
		}
		return extraImages[i].Tag < extraImages[j].Tag
	})
	fmt.Fprintf(os.Stderr, "resolving %d static extra image(s)\n", len(extraImages))
	for i, extra := range extraImages {
		imageRef := extra.Repo + ":" + extra.Tag
		fmt.Fprintf(os.Stderr, "[static] resolving image %d/%d: %s\n", i+1, len(extraImages), imageRef)
		items, err := resolveImage("", imageRef)
		if err != nil {
			return err
		}
		images = append(images, items...)
	}

	fmt.Fprintf(os.Stderr, "scanning %d non-CRD charts\n", len(chartNames))
	for i, component := range chartNames {
		fmt.Fprintf(os.Stderr, "[%d/%d] rendering %s\n", i+1, len(chartNames), component)
		chartImages, err := chartImages(filepath.Join(chartsDir, component))
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "[%d/%d] found %d image(s) in %s\n", i+1, len(chartNames), len(chartImages), component)
		for j, chartImage := range chartImages {
			fmt.Fprintf(os.Stderr, "[%d/%d] resolving image %d/%d: %s\n", i+1, len(chartNames), j+1, len(chartImages), chartImage)
			items, err := resolveImage(component, chartImage)
			if err != nil {
				return err
			}
			images = append(images, items...)
		}
	}

	sort.Slice(images, func(i, j int) bool {
		if images[i].Component != images[j].Component {
			return images[i].Component < images[j].Component
		}
		if images[i].Image != images[j].Image {
			return images[i].Image < images[j].Image
		}
		return images[i].Platform < images[j].Platform
	})
	manifestVersion := *version
	if manifestVersion == "" {
		manifestVersion = os.Getenv("VERSION")
	}
	if manifestVersion == "" {
		manifestVersion = "dev"
	}
	body, err := json.MarshalIndent(manifest{Components: components{Version: manifestVersion, Images: images}}, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*outputPath, body, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d images)\n", *outputPath, len(images))
	return nil
}

// resolveImage resolves a source image reference to one entry per supported
// platform. For multi-platform indexes, digest is the platform manifest digest
// and indexDigest records the resolved upstream index digest.
func resolveImage(component, sourceImage string) ([]image, error) {
	metadata := componentMetadata[component]
	repo, tag, digest := splitRef(sourceImage)
	resolvedImage := repo + "@" + digest
	if digest == "" {
		if tag == "" {
			tag = "latest"
		}
		resolvedDigest, err := commandOutput("crane", "digest", repo+":"+tag)
		if err != nil {
			return nil, fmt.Errorf("resolve digest for %s: %w", sourceImage, err)
		}
		digest = strings.TrimSpace(resolvedDigest)
		resolvedImage = repo + "@" + digest
	}
	fmt.Fprintf(os.Stderr, "inspecting manifest for %s\n", resolvedImage)
	body, err := commandOutput("crane", "manifest", resolvedImage)
	if err != nil {
		return nil, fmt.Errorf("inspect manifest for %s: %w", resolvedImage, err)
	}
	index, err := parseImageIndex([]byte(body))
	if err != nil {
		return nil, fmt.Errorf("parse manifest for %s: %w", resolvedImage, err)
	}
	if len(index.Manifests) == 0 {
		size, err := manifestSize([]byte(body))
		if err != nil {
			return nil, fmt.Errorf("calculate image size for %s: %w", resolvedImage, err)
		}
		return []image{{Component: component, Image: sourceImage, Digest: digest, Size: size, Providers: metadata.Providers, Addon: metadata.Addon}}, nil
	}

	items := []image{}
	for _, child := range index.Manifests {
		platform := platformString(child.Platform)
		if !supportedPlatform(platform) {
			continue
		}
		childRef := repo + "@" + child.Digest
		fmt.Fprintf(os.Stderr, "calculating size for %s (%s)\n", childRef, platform)
		childBody, err := commandOutput("crane", "manifest", childRef)
		if err != nil {
			return nil, fmt.Errorf("inspect child manifest %s: %w", childRef, err)
		}
		size, err := manifestSize([]byte(childBody))
		if err != nil {
			return nil, fmt.Errorf("calculate image size for %s: %w", childRef, err)
		}
		items = append(items, image{
			Component: component,
			Image:     sourceImage,
			Digest:    child.Digest,
			Size:      size,
			Platform:  platform,
			Index:     digest,
			Providers: metadata.Providers,
			Addon:     metadata.Addon,
		})
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s has no supported linux/amd64 or linux/arm64 platform", sourceImage)
	}
	return items, nil
}

// chartImages renders a chart and extracts normalized images from pod specs.
func chartImages(chartPath string) ([]string, error) {
	rendered, err := commandOutput("helm", "template", chartPath)
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", chartPath, err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(rendered))
	seen := map[string]bool{}
	for {
		var doc any
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse rendered YAML for %s: %w", chartPath, err)
		}
		if isTestHook(doc) {
			continue
		}
		walkPodSpecs(doc, func(podSpec map[string]any) {
			for _, key := range []string{"initContainers", "containers", "ephemeralContainers"} {
				for _, container := range listOfMaps(podSpec[key]) {
					if value, ok := container["image"].(string); ok && value != "" {
						seen[normalizeImage(value)] = true
					}
				}
			}
		})
	}
	images := make([]string, 0, len(seen))
	for value := range seen {
		images = append(images, value)
	}
	sort.Strings(images)
	return images, nil
}

// isTestHook reports whether a rendered object is a Helm test hook.
func isTestHook(doc any) bool {
	root, ok := doc.(map[string]any)
	if !ok {
		return false
	}
	metadata, ok := root["metadata"].(map[string]any)
	if !ok {
		return false
	}
	annotations, ok := metadata["annotations"].(map[string]any)
	if !ok {
		return false
	}
	hook, ok := annotations["helm.sh/hook"].(string)
	if !ok {
		return false
	}
	for _, part := range strings.Split(hook, ",") {
		switch strings.TrimSpace(part) {
		case "test", "test-success", "test-failure":
			return true
		}
	}
	return false
}

// walkPodSpecs recursively visits maps that look like Kubernetes PodSpecs.
func walkPodSpecs(value any, visit func(map[string]any)) {
	switch v := value.(type) {
	case map[string]any:
		if _, ok := v["containers"]; ok {
			visit(v)
		} else if _, ok := v["initContainers"]; ok {
			visit(v)
		} else if _, ok := v["ephemeralContainers"]; ok {
			visit(v)
		}
		for _, child := range v {
			walkPodSpecs(child, visit)
		}
	case []any:
		for _, child := range v {
			walkPodSpecs(child, visit)
		}
	}
}

// listOfMaps converts a decoded YAML array to a slice of object maps.
func listOfMaps(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	maps := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if value, ok := item.(map[string]any); ok {
			maps = append(maps, value)
		}
	}
	return maps
}

// normalizeImage expands Docker Hub shorthand to fully-qualified references.
func normalizeImage(value string) string {
	value = strings.TrimSpace(value)
	first := value
	if slash := strings.Index(value, "/"); slash >= 0 {
		first = value[:slash]
	}
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		return value
	}
	if strings.Contains(value, "/") {
		return "docker.io/" + value
	}
	return "docker.io/library/" + value
}

// splitRef separates an image reference into repository, tag, and digest parts.
func splitRef(value string) (repo string, tag string, digest string) {
	if before, after, ok := strings.Cut(value, "@"); ok {
		value = before
		digest = after
	}
	lastSlash := strings.LastIndex(value, "/")
	lastColon := strings.LastIndex(value, ":")
	if lastColon > lastSlash {
		tag = value[lastColon+1:]
		value = value[:lastColon]
	}
	return value, tag, digest
}

type imageIndex struct {
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
			Variant      string `json:"variant"`
		} `json:"platform"`
	} `json:"manifests"`
}

func parseImageIndex(body []byte) (imageIndex, error) {
	var index imageIndex
	if err := json.Unmarshal(body, &index); err != nil {
		return imageIndex{}, err
	}
	return index, nil
}

func platformString(platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant"`
}) string {
	if platform.OS == "" || platform.Architecture == "" || platform.OS == "unknown" || platform.Architecture == "unknown" {
		return ""
	}
	value := platform.OS + "/" + platform.Architecture
	if platform.Variant != "" {
		value += "/" + platform.Variant
	}
	return value
}

func supportedPlatform(platform string) bool {
	if supportedPlatforms[platform] {
		return true
	}
	return strings.HasPrefix(platform, "linux/arm64/")
}

// manifestSize calculates size for one platform manifest.
func manifestSize(body []byte) (int64, error) {
	var manifest struct {
		Config *struct {
			Size int64 `json:"size"`
		} `json:"config"`
		Layers []struct {
			Size int64 `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return 0, err
	}
	size := int64(len(body))
	if manifest.Config != nil {
		size += manifest.Config.Size
	}
	for _, layer := range manifest.Layers {
		size += layer.Size
	}
	return size, nil
}

// commandOutput runs a command and returns stdout with stderr in errors.
func commandOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
