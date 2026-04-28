package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListProjectsIncludesComposeVariants(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	apiDir := filepath.Join(root, "api")
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		t.Fatalf("mkdir api: %v", err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, "package.json"), []byte(`{"scripts":{"dev":"vite"}}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, "docker-compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write docker-compose.yaml: %v", err)
	}

	infraDir := filepath.Join(root, "infra")
	if err := os.MkdirAll(infraDir, 0o755); err != nil {
		t.Fatalf("mkdir infra: %v", err)
	}
	if err := os.WriteFile(filepath.Join(infraDir, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write compose.yaml: %v", err)
	}

	projects, err := listProjects([]string{root})
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}

	displays := make(map[string]projectEntry, len(projects))
	for _, project := range projects {
		displays[project.Display] = project
	}

	if _, ok := displays["api"]; !ok {
		t.Fatalf("expected main api entry, got %#v", displays)
	}
	composeEntry, ok := displays["api (compose)"]
	if !ok {
		t.Fatalf("expected compose api entry, got %#v", displays)
	}
	if composeEntry.Variant != projectVariantCompose {
		t.Fatalf("expected compose variant, got %q", composeEntry.Variant)
	}
	if _, ok := displays["infra"]; ok {
		t.Fatalf("did not expect plain infra entry for compose-only project")
	}
	if _, ok := displays["infra (compose)"]; !ok {
		t.Fatalf("expected compose-only infra entry, got %#v", displays)
	}
	if got := buildProjectStackMap(projects)["api (compose)"]; got != "🐳 docker compose" {
		t.Fatalf("unexpected compose stack preview: %q", got)
	}
}

func TestResolveLaunchpadProjectsKeepsVariantsFromSamePath(t *testing.T) {
	t.Parallel()

	entryPath := filepath.Join(t.TempDir(), "api")
	entries := []projectEntry{
		{Name: "api", Path: entryPath, Display: "api"},
		{Name: "api", Path: entryPath, Display: "api (compose)", Variant: projectVariantCompose},
	}

	resolved := resolveLaunchpadProjects([]string{"api", "api (compose)"}, entries)
	if len(resolved) != 2 {
		t.Fatalf("expected both entries to resolve, got %#v", resolved)
	}
	if resolved[0].Display != "api" || resolved[1].Display != "api (compose)" {
		t.Fatalf("unexpected resolution order: %#v", resolved)
	}
}

func TestChooseComposeProjectRejectsAmbiguousBareName(t *testing.T) {
	t.Parallel()

	entries := []projectEntry{
		{Name: "api", Display: "api (/work/apps) (compose)", Variant: projectVariantCompose},
		{Name: "api", Display: "api (/work/client) (compose)", Variant: projectVariantCompose},
	}

	_, err := chooseComposeProject(entries, "api", strings.NewReader(""), &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "multiple compose projects match") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "api (/work/apps) (compose)") || !strings.Contains(err.Error(), "api (/work/client) (compose)") {
		t.Fatalf("expected disambiguation options in error, got: %v", err)
	}
}
