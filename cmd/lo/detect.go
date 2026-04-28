package main

// Runtime detection, framework hinting, and package manager resolution for
// all supported languages: Node.js, Rust, Go, Python, and Java.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"golang.org/x/term"
)

// --- Project manifest types ---

type packageJSON struct {
	Name         string            `json:"name"`
	Scripts      map[string]string `json:"scripts"`
	Dependencies map[string]string `json:"dependencies"`
	DevDepends   map[string]string `json:"devDependencies"`
	PeerDepends  map[string]string `json:"peerDependencies"`
	OptionalDeps map[string]string `json:"optionalDependencies"`
}

type pyprojectToml struct {
	Project struct {
		Dependencies         []string            `toml:"dependencies"`
		OptionalDependencies map[string][]string `toml:"optional-dependencies"`
	} `toml:"project"`
	Tool struct {
		Poetry struct {
			Dependencies map[string]any `toml:"dependencies"`
			Group        map[string]struct {
				Dependencies map[string]any `toml:"dependencies"`
			} `toml:"group"`
		} `toml:"poetry"`
	} `toml:"tool"`
}

const (
	projectVariantStorybook = "storybook"
	projectVariantCompose   = "compose"
)

// --- Runnability check ---

// isRunnableProjectDir returns true if the directory looks like a launchable project.
func isRunnableProjectDir(projectPath string) bool {
	entries, err := os.ReadDir(projectPath)
	if err != nil || len(entries) == 0 {
		return false
	}

	packageJSONPath := filepath.Join(projectPath, "package.json")
	if fileExists(packageJSONPath) {
		pkg, err := loadPackageJSON(packageJSONPath)
		if err != nil {
			return false
		}
		return detectScript(pkg) != ""
	}

	if fileExists(filepath.Join(projectPath, "Cargo.toml")) {
		return true
	}

	if isGoProject(projectPath) || isJavaProject(projectPath) {
		return true
	}

	if isPythonProject(projectPath) {
		_, err := detectPythonEntrypoint(projectPath)
		return err == nil
	}

	return false
}

func hasComposeFile(projectPath string) bool {
	_, ok := detectComposeFile(projectPath)
	return ok
}

func detectComposeFile(projectPath string) (string, bool) {
	for _, name := range []string{"docker-compose.yaml", "docker-compose.yml", "compose.yaml", "compose.yml"} {
		if fileExists(filepath.Join(projectPath, name)) {
			return name, true
		}
	}
	return "", false
}

// --- Stack preview (shown in the picker) ---

// previewProjectStack returns a short human-readable stack description.
func previewProjectStack(projectPath string) string {
	if fileExists(filepath.Join(projectPath, "package.json")) {
		return previewNodeStack(projectPath)
	}

	if fileExists(filepath.Join(projectPath, "Cargo.toml")) {
		framework := detectRustFramework(projectPath)
		return fmt.Sprintf("🦀 rust / %s", framework)
	}

	if isGoProject(projectPath) {
		framework := detectGoFramework(projectPath)
		return fmt.Sprintf("🐹 go / %s", framework)
	}

	if isJavaProject(projectPath) {
		framework := detectJavaFramework(projectPath)
		return fmt.Sprintf("☕ java / %s", framework)
	}

	if isPythonProject(projectPath) {
		framework := detectPythonFramework(projectPath)
		return fmt.Sprintf("🐍 python / %s", framework)
	}

	return ""
}

func previewNodeStack(projectPath string) string {
	pkgPath := filepath.Join(projectPath, "package.json")
	pkg, err := loadPackageJSON(pkgPath)
	if err != nil {
		return " node / invalid package.json"
	}

	framework := detectNodeFramework(pkg)
	return fmt.Sprintf(" node / %s", framework)
}

// --- Main runner detection entry point ---

// detectProjectRunner determines the target label, optional install command,
// and run command for a given project directory. If scriptOverride is non-empty
// it is used as the npm script instead of auto-detecting dev/start.
// in/out are passed through for interactive package-manager selection when no
// lockfile is found; either may be nil for non-interactive callers.
func detectProjectRunner(projectPath, variant, scriptOverride string, in io.Reader, out io.Writer) (target string, installCmd []string, runCmd []string, err error) {
	if variant == projectVariantCompose {
		composeFile, ok := detectComposeFile(projectPath)
		if !ok {
			return "", nil, nil, errors.New("❌ no docker compose file found in project root")
		}
		if !hasCommand("docker") {
			return "", nil, nil, errors.New("❌ missing dependency: docker. Install Docker and run again")
		}
		composeCheck := exec.Command("docker", "compose", "version")
		if err := composeCheck.Run(); err != nil {
			return "", nil, nil, errors.New("❌ missing dependency: docker compose. Install the Docker Compose plugin and run again")
		}
		return "docker compose", nil, []string{"docker", "compose", "-f", composeFile, "up", "-d", "--build", "--remove-orphans"}, nil
	}

	packageJSONPath := filepath.Join(projectPath, "package.json")
	if _, statErr := os.Stat(packageJSONPath); statErr == nil {
		pkg, readErr := loadPackageJSON(packageJSONPath)
		if readErr != nil {
			return "", nil, nil, readErr
		}

		script := scriptOverride
		if script == "" {
			script = detectScript(pkg)
		}
		if script == "" {
			return "", nil, nil, errors.New("❌ no dev/start script found in package.json")
		}

		pm, install, run, pmErr := detectPackageManager(projectPath, script, in, out)
		if pmErr != nil {
			return "", nil, nil, pmErr
		}

		nodeModulesPath := filepath.Join(projectPath, "node_modules")
		if _, statErr := os.Stat(nodeModulesPath); errors.Is(statErr, os.ErrNotExist) {
			return fmt.Sprintf("%s (%s)", pm, script), install, run, nil
		}

		return fmt.Sprintf("%s (%s)", pm, script), nil, run, nil
	}

	cargoPath := filepath.Join(projectPath, "Cargo.toml")
	if _, statErr := os.Stat(cargoPath); statErr == nil {
		if !hasCommand("cargo") {
			return "", nil, nil, errors.New("❌ missing dependency: cargo. Install Rust toolchain and run again")
		}
		var install []string
		// Fetch crates on first clone (no Cargo.lock yet).
		if !fileExists(filepath.Join(projectPath, "Cargo.lock")) {
			install = []string{"cargo", "fetch"}
		}
		return "cargo (run)", install, []string{"cargo", "run"}, nil
	}

	if isGoProject(projectPath) {
		return detectGoRunner(projectPath)
	}

	if isJavaProject(projectPath) {
		return detectJavaRunner(projectPath)
	}

	if isPythonProject(projectPath) {
		return detectPythonRunner(projectPath)
	}

	return "", nil, nil, errors.New("❌ unsupported project type. Expected package.json, Cargo.toml, go.mod, pom.xml/build.gradle, pyproject.toml, .py entrypoint, or docker compose file")
}

// --- Node.js ---

func loadPackageJSON(path string) (packageJSON, error) {
	var pkg packageJSON
	data, err := os.ReadFile(path)
	if err != nil {
		return pkg, fmt.Errorf("❌ package.json not found in %s", filepath.Dir(path))
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return pkg, fmt.Errorf("❌ invalid package.json: %w", err)
	}
	return pkg, nil
}

// detectScript returns the preferred npm script to run: dev, start, docs:dev, or storybook.
func detectScript(pkg packageJSON) string {
	if pkg.Scripts == nil {
		return ""
	}
	if _, ok := pkg.Scripts["dev"]; ok {
		return "dev"
	}
	if _, ok := pkg.Scripts["start"]; ok {
		return "start"
	}
	if _, ok := pkg.Scripts["docs:dev"]; ok {
		return "docs:dev"
	}
	if _, ok := pkg.Scripts["storybook"]; ok {
		return "storybook"
	}
	return ""
}

func detectNodeFramework(pkg packageJSON) string {
	deps := make(map[string]struct{})
	for k := range pkg.Dependencies {
		deps[strings.ToLower(k)] = struct{}{}
	}
	for k := range pkg.DevDepends {
		deps[strings.ToLower(k)] = struct{}{}
	}
	for k := range pkg.PeerDepends {
		deps[strings.ToLower(k)] = struct{}{}
	}
	for k := range pkg.OptionalDeps {
		deps[strings.ToLower(k)] = struct{}{}
	}

	has := func(name string) bool {
		_, ok := deps[name]
		return ok
	}

	switch {
	case has("next"):
		return "next"
	case has("nuxt") || has("nuxt3"):
		return "nuxt"
	case has("@sveltejs/kit"):
		return "sveltekit"
	case has("astro"):
		return "astro"
	case has("@nestjs/core"):
		return "nestjs"
	case has("remix") || has("@remix-run/react"):
		return "remix"
	case has("vite") && has("react"):
		return "vite+react"
	case has("vite") && has("vue"):
		return "vite+vue"
	case has("vite"):
		return "vite"
	case has("react"):
		return "react"
	case has("vue"):
		return "vue"
	case has("@angular/core") || has("angular"):
		return "angular"
	case has("express"):
		return "express"
	case has("fastify"):
		return "fastify"
	case has("hono"):
		return "hono"
	case has("@docusaurus/core"):
		return "docusaurus"
	case has("vuepress") || has("@vuepress/core") || has("vuepress-vite"):
		return "vuepress"
	case hasStorybookDep(deps):
		return "storybook"
	default:
		return "node"
	}
}

// hasStorybookDep returns true when any @storybook/* package or the storybook CLI is present.
func hasStorybookDep(deps map[string]struct{}) bool {
	if _, ok := deps["storybook"]; ok {
		return true
	}
	for k := range deps {
		if strings.HasPrefix(k, "@storybook/") {
			return true
		}
	}
	return false
}

func detectPackageManagerFromLockfile(projectPath string) string {
	switch {
	case fileExists(filepath.Join(projectPath, "pnpm-lock.yaml")):
		return "pnpm"
	case fileExists(filepath.Join(projectPath, "bun.lock")) || fileExists(filepath.Join(projectPath, "bun.lockb")):
		return "bun"
	case fileExists(filepath.Join(projectPath, "package-lock.json")):
		return "npm"
	case fileExists(filepath.Join(projectPath, "yarn.lock")):
		return "yarn"
	default:
		return "unknown"
	}
}

func detectPackageManager(projectPath, script string, in io.Reader, out io.Writer) (pm string, installCmd []string, runCmd []string, err error) {
	type pmDef struct {
		name    string
		lock    string
		run     []string
		install []string
	}

	defs := []pmDef{
		{name: "pnpm", lock: "pnpm-lock.yaml", run: []string{"pnpm", script}, install: []string{"pnpm", "install"}},
		{name: "bun", lock: "bun.lock", run: []string{"bun", script}, install: []string{"bun", "install"}},
		{name: "bun", lock: "bun.lockb", run: []string{"bun", script}, install: []string{"bun", "install"}},
		{name: "npm", lock: "package-lock.json", run: []string{"npm", "run", script}, install: []string{"npm", "install"}},
		{name: "yarn", lock: "yarn.lock", run: []string{"yarn", script}, install: []string{"yarn", "install"}},
	}

	for _, def := range defs {
		if _, statErr := os.Stat(filepath.Join(projectPath, def.lock)); statErr == nil {
			if !hasCommand(def.name) {
				return "", nil, nil, fmt.Errorf("❌ missing dependency: %s. Install it and run again", def.name)
			}
			return def.name, def.install, def.run, nil
		}
	}

	// No lockfile — ask the user which package manager to use.
	return promptNodePackageManager(script, in, out)
}

// promptNodePackageManager presents a numbered list of available Node.js package
// managers and returns the user's choice. Falls back to the first available PM
// when running non-interactively or only one option exists.
func promptNodePackageManager(script string, in io.Reader, out io.Writer) (pm string, installCmd []string, runCmd []string, err error) {
	type pmOpt struct {
		name    string
		install []string
		run     []string
	}

	all := []pmOpt{
		{"pnpm", []string{"pnpm", "install"}, []string{"pnpm", script}},
		{"bun", []string{"bun", "install"}, []string{"bun", script}},
		{"npm", []string{"npm", "install"}, []string{"npm", "run", script}},
		{"yarn", []string{"yarn", "install"}, []string{"yarn", script}},
	}

	var available []pmOpt
	for _, c := range all {
		if hasCommand(c.name) {
			available = append(available, c)
		}
	}

	if len(available) == 0 {
		return "", nil, nil, errors.New("❌ no Node.js package manager found. Install npm, pnpm, yarn, or bun and run again")
	}

	// Only one option or non-interactive: use the first available silently.
	if len(available) == 1 {
		c := available[0]
		return c.name, c.install, c.run, nil
	}

	inFile, inOk := in.(*os.File)
	outFile, outOk := out.(*os.File)
	if !inOk || !outOk || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		c := available[0]
		return c.name, c.install, c.run, nil
	}

	fmt.Fprintln(out, "⚠️  No lockfile found. Which package manager should be used?")
	for i, c := range available {
		fmt.Fprintf(out, "  %d) %s\n", i+1, c.name)
	}
	fmt.Fprint(out, "\n󰍉 Choice [1]: ")

	reader := bufio.NewReader(in)
	line, readErr := reader.ReadString('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		c := available[0]
		return c.name, c.install, c.run, nil
	}

	line = strings.TrimSpace(line)
	if line == "" {
		c := available[0]
		return c.name, c.install, c.run, nil
	}

	n, convErr := strconv.Atoi(line)
	if convErr != nil || n < 1 || n > len(available) {
		c := available[0]
		fmt.Fprintf(out, "⚠️  Invalid choice, using %s\n", c.name)
		return c.name, c.install, c.run, nil
	}

	c := available[n-1]
	return c.name, c.install, c.run, nil
}

// --- Rust ---

func detectRustFramework(projectPath string) string {
	content := strings.ToLower(readFileOrEmpty(filepath.Join(projectPath, "Cargo.toml")))
	switch {
	case strings.Contains(content, "axum"):
		return "axum"
	case strings.Contains(content, "actix-web"):
		return "actix"
	case strings.Contains(content, "rocket"):
		return "rocket"
	case strings.Contains(content, "tauri"):
		return "tauri"
	case strings.Contains(content, "bevy"):
		return "bevy"
	default:
		return "cargo"
	}
}

// --- Go ---

func isGoProject(projectPath string) bool {
	return fileExists(filepath.Join(projectPath, "go.mod"))
}

func detectGoFramework(projectPath string) string {
	content := strings.ToLower(readFileOrEmpty(filepath.Join(projectPath, "go.mod")))
	switch {
	case strings.Contains(content, "github.com/gin-gonic/gin"):
		return "gin"
	case strings.Contains(content, "github.com/gofiber/fiber"):
		return "fiber"
	case strings.Contains(content, "github.com/labstack/echo"):
		return "echo"
	case strings.Contains(content, "github.com/go-chi/chi"):
		return "chi"
	case strings.Contains(content, "go.temporal.io"):
		return "temporal"
	default:
		return "go"
	}
}

func detectGoEntryHint(projectPath string) string {
	if hasMainPackageInDir(projectPath) {
		return "go run ."
	}

	cmdDir := filepath.Join(projectPath, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return "go run ."
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sub := filepath.Join(cmdDir, entry.Name())
		if hasMainPackageInDir(sub) {
			return fmt.Sprintf("go run ./cmd/%s", entry.Name())
		}
	}

	return "go run ."
}

func hasMainPackageInDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		content := strings.ToLower(readFileOrEmpty(filepath.Join(dir, entry.Name())))
		if strings.Contains(content, "package main") {
			return true
		}
	}
	return false
}

func detectGoRunner(projectPath string) (target string, installCmd []string, runCmd []string, err error) {
	if !hasCommand("go") {
		return "", nil, nil, errors.New("❌ missing dependency: go. Install Go and run again")
	}

	// Download module dependencies when they are not vendored yet.
	var install []string
	if fileExists(filepath.Join(projectPath, "go.sum")) && !fileExists(filepath.Join(projectPath, "vendor")) {
		install = []string{"go", "mod", "download"}
	}

	entry := detectGoEntryHint(projectPath)
	framework := detectGoFramework(projectPath)

	if strings.HasPrefix(entry, "go run ./cmd/") {
		cmdPath := strings.TrimPrefix(entry, "go run ")
		return fmt.Sprintf("go (%s)", framework), install, []string{"go", "run", cmdPath}, nil
	}

	return fmt.Sprintf("go (%s)", framework), install, []string{"go", "run", "."}, nil
}

// --- Java ---

func isJavaProject(projectPath string) bool {
	if fileExists(filepath.Join(projectPath, "pom.xml")) || fileExists(filepath.Join(projectPath, "build.gradle")) || fileExists(filepath.Join(projectPath, "build.gradle.kts")) {
		return true
	}

	return fileExists(filepath.Join(projectPath, "src", "main", "java"))
}

func detectJavaFramework(projectPath string) string {
	content := strings.ToLower(
		readFileOrEmpty(filepath.Join(projectPath, "pom.xml")) + "\n" +
			readFileOrEmpty(filepath.Join(projectPath, "build.gradle")) + "\n" +
			readFileOrEmpty(filepath.Join(projectPath, "build.gradle.kts")),
	)

	switch {
	case strings.Contains(content, "spring-boot") || strings.Contains(content, "org.springframework.boot"):
		return "spring"
	case strings.Contains(content, "quarkus"):
		return "quarkus"
	case strings.Contains(content, "micronaut"):
		return "micronaut"
	default:
		return "java"
	}
}

func detectJavaRunnerHint(projectPath string) string {
	if fileExists(filepath.Join(projectPath, "mvnw")) || fileExists(filepath.Join(projectPath, "mvnw.cmd")) {
		if detectJavaFramework(projectPath) == "spring" {
			return "mvn spring-boot:run"
		}
		return "mvn exec"
	}
	if fileExists(filepath.Join(projectPath, "gradlew")) || fileExists(filepath.Join(projectPath, "gradlew.bat")) {
		if detectJavaFramework(projectPath) == "spring" {
			return "gradle bootRun"
		}
		return "gradle run"
	}
	if fileExists(filepath.Join(projectPath, "pom.xml")) {
		return "mvn"
	}
	if fileExists(filepath.Join(projectPath, "build.gradle")) || fileExists(filepath.Join(projectPath, "build.gradle.kts")) {
		return "gradle"
	}
	return "java"
}

func detectJavaRunner(projectPath string) (target string, installCmd []string, runCmd []string, err error) {
	framework := detectJavaFramework(projectPath)

	if fileExists(filepath.Join(projectPath, "pom.xml")) {
		mvnCmd, err := detectMavenCommand(projectPath)
		if err != nil {
			return "", nil, nil, err
		}

		if framework == "spring" {
			return "java (spring/maven)", nil, []string{mvnCmd, "spring-boot:run"}, nil
		}

		return "java (maven)", nil, []string{mvnCmd, "exec:java"}, nil
	}

	if fileExists(filepath.Join(projectPath, "build.gradle")) || fileExists(filepath.Join(projectPath, "build.gradle.kts")) {
		gradleCmd, err := detectGradleCommand(projectPath)
		if err != nil {
			return "", nil, nil, err
		}

		if framework == "spring" {
			return "java (spring/gradle)", nil, []string{gradleCmd, "bootRun"}, nil
		}

		if hasGradleApplicationPlugin(projectPath) {
			return "java (gradle)", nil, []string{gradleCmd, "run"}, nil
		}

		return "java (gradle)", nil, []string{gradleCmd, "build"}, nil
	}

	return "", nil, nil, errors.New("❌ java project detected but no pom.xml/build.gradle found")
}

func detectMavenCommand(projectPath string) (string, error) {
	if runtime.GOOS == "windows" && fileExists(filepath.Join(projectPath, "mvnw.cmd")) {
		return "mvnw.cmd", nil
	}
	if fileExists(filepath.Join(projectPath, "mvnw")) {
		return "./mvnw", nil
	}
	if hasCommand("mvn") {
		return "mvn", nil
	}
	return "", errors.New("❌ missing dependency: maven. Install mvn or add mvnw wrapper")
}

func detectGradleCommand(projectPath string) (string, error) {
	if runtime.GOOS == "windows" && fileExists(filepath.Join(projectPath, "gradlew.bat")) {
		return "gradlew.bat", nil
	}
	if fileExists(filepath.Join(projectPath, "gradlew")) {
		return "./gradlew", nil
	}
	if hasCommand("gradle") {
		return "gradle", nil
	}
	return "", errors.New("❌ missing dependency: gradle. Install gradle or add gradlew wrapper")
}

func hasGradleApplicationPlugin(projectPath string) bool {
	content := strings.ToLower(
		readFileOrEmpty(filepath.Join(projectPath, "build.gradle")) + "\n" +
			readFileOrEmpty(filepath.Join(projectPath, "build.gradle.kts")),
	)
	return strings.Contains(content, "application")
}

// --- Python ---

func isPythonProject(projectPath string) bool {
	if fileExists(filepath.Join(projectPath, "pyproject.toml")) || fileExists(filepath.Join(projectPath, "requirements.txt")) || fileExists(filepath.Join(projectPath, "setup.py")) {
		return true
	}

	if fileExists(filepath.Join(projectPath, "mkdocs.yml")) || fileExists(filepath.Join(projectPath, "mkdocs.yaml")) {
		return true
	}

	pythonMarkers := []string{"main.py", "app.py", "manage.py", "wsgi.py"}
	for _, marker := range pythonMarkers {
		if fileExists(filepath.Join(projectPath, marker)) {
			return true
		}
	}

	return false
}

func detectPythonFramework(projectPath string) string {
	if fileExists(filepath.Join(projectPath, "manage.py")) {
		return "django"
	}

	if fileExists(filepath.Join(projectPath, "mkdocs.yml")) || fileExists(filepath.Join(projectPath, "mkdocs.yaml")) {
		return "mkdocs"
	}

	deps := make(map[string]struct{})
	for _, dep := range parsePythonDependencies(projectPath) {
		deps[strings.ToLower(dep)] = struct{}{}
	}

	has := func(name string) bool {
		_, ok := deps[name]
		return ok
	}

	switch {
	case has("fastapi"):
		return "fastapi"
	case has("flask"):
		return "flask"
	case has("django"):
		return "django"
	case has("streamlit"):
		return "streamlit"
	case has("gradio"):
		return "gradio"
	default:
		return "python"
	}
}

func detectPythonRunnerHint(projectPath string) string {
	if fileExists(filepath.Join(projectPath, "manage.py")) {
		return "manage.py runserver"
	}
	if fileExists(filepath.Join(projectPath, "main.py")) {
		return "main.py"
	}
	if fileExists(filepath.Join(projectPath, "app.py")) {
		return "app.py"
	}
	if fileExists(filepath.Join(projectPath, "uv.lock")) {
		return "uv run"
	}
	if fileExists(filepath.Join(projectPath, "poetry.lock")) {
		return "poetry run"
	}
	return "python"
}

func detectPythonRunner(projectPath string) (target string, installCmd []string, runCmd []string, err error) {
	pythonCmd, err := detectPythonCommand()
	if err != nil {
		return "", nil, nil, err
	}

	entry, err := detectPythonEntrypoint(projectPath)
	if err != nil {
		return "", nil, nil, err
	}

	framework := detectPythonFramework(projectPath)

	// MkDocs runs as a standalone command, not via the python interpreter.
	if framework == "mkdocs" {
		if !hasCommand("mkdocs") {
			return "", nil, nil, errors.New("❌ missing dependency: mkdocs. Install with 'pip install mkdocs' and run again")
		}
		return "mkdocs (serve)", nil, entry, nil
	}

	if fileExists(filepath.Join(projectPath, "uv.lock")) && hasCommand("uv") {
		run := append([]string{"uv", "run", "python"}, entry...)
		return fmt.Sprintf("python (%s, uv)", framework), []string{"uv", "sync"}, run, nil
	}

	if fileExists(filepath.Join(projectPath, "poetry.lock")) && hasCommand("poetry") {
		run := append([]string{"poetry", "run", "python"}, entry...)
		return fmt.Sprintf("python (%s, poetry)", framework), []string{"poetry", "install"}, run, nil
	}

	run := append([]string{pythonCmd}, entry...)
	if fileExists(filepath.Join(projectPath, "requirements.txt")) {
		install := []string{pythonCmd, "-m", "pip", "install", "-r", "requirements.txt"}
		return fmt.Sprintf("python (%s)", framework), install, run, nil
	}

	// setup.py / pyproject.toml without a dedicated lockfile: editable install.
	if fileExists(filepath.Join(projectPath, "setup.py")) || fileExists(filepath.Join(projectPath, "pyproject.toml")) {
		install := []string{pythonCmd, "-m", "pip", "install", "-e", "."}
		return fmt.Sprintf("python (%s)", framework), install, run, nil
	}

	return fmt.Sprintf("python (%s)", framework), nil, run, nil
}

func detectPythonEntrypoint(projectPath string) ([]string, error) {
	if fileExists(filepath.Join(projectPath, "manage.py")) {
		return []string{"manage.py", "runserver"}, nil
	}
	if fileExists(filepath.Join(projectPath, "mkdocs.yml")) || fileExists(filepath.Join(projectPath, "mkdocs.yaml")) {
		return []string{"mkdocs", "serve"}, nil
	}
	if fileExists(filepath.Join(projectPath, "main.py")) {
		return []string{"main.py"}, nil
	}
	if fileExists(filepath.Join(projectPath, "app.py")) {
		return []string{"app.py"}, nil
	}
	if fileExists(filepath.Join(projectPath, "wsgi.py")) {
		return []string{"wsgi.py"}, nil
	}

	moduleName := sanitizeModuleName(filepath.Base(projectPath))
	if moduleName != "" {
		if fileExists(filepath.Join(projectPath, moduleName, "__main__.py")) || fileExists(filepath.Join(projectPath, "src", moduleName, "__main__.py")) {
			return []string{"-m", moduleName}, nil
		}
	}

	return nil, errors.New("❌ python project detected but no entrypoint found (main.py, app.py, manage.py, or package __main__.py)")
}

func sanitizeModuleName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}

func detectPythonCommand() (string, error) {
	if hasCommand("python3") {
		return "python3", nil
	}
	if hasCommand("python") {
		return "python", nil
	}
	if runtime.GOOS == "windows" && hasCommand("py") {
		return "py", nil
	}
	return "", errors.New("❌ missing dependency: python. Install Python and run again")
}

// --- Python dependency parsing ---

func parsePythonDependencies(projectPath string) []string {
	deps := make(map[string]struct{})

	pyprojectPath := filepath.Join(projectPath, "pyproject.toml")
	if fileExists(pyprojectPath) {
		if parsed, err := loadPyproject(pyprojectPath); err == nil {
			for _, dep := range parsed {
				if dep != "" {
					deps[strings.ToLower(dep)] = struct{}{}
				}
			}
		}
	}

	reqPath := filepath.Join(projectPath, "requirements.txt")
	for _, dep := range parseRequirements(reqPath) {
		deps[strings.ToLower(dep)] = struct{}{}
	}

	out := make([]string, 0, len(deps))
	for dep := range deps {
		out = append(out, dep)
	}
	sort.Strings(out)
	return out
}

func loadPyproject(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg pyprojectToml
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	deps := make(map[string]struct{})
	for _, raw := range cfg.Project.Dependencies {
		name := parsePythonDependencyName(raw)
		if name != "" {
			deps[name] = struct{}{}
		}
	}
	for _, groupDeps := range cfg.Project.OptionalDependencies {
		for _, raw := range groupDeps {
			name := parsePythonDependencyName(raw)
			if name != "" {
				deps[name] = struct{}{}
			}
		}
	}
	for name := range cfg.Tool.Poetry.Dependencies {
		if strings.EqualFold(name, "python") {
			continue
		}
		deps[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	for _, group := range cfg.Tool.Poetry.Group {
		for name := range group.Dependencies {
			if strings.EqualFold(name, "python") {
				continue
			}
			deps[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
		}
	}

	out := make([]string, 0, len(deps))
	for name := range deps {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func parseRequirements(path string) []string {
	content := readFileOrEmpty(path)
	if content == "" {
		return nil
	}

	deps := make([]string, 0)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := parsePythonDependencyName(line)
		if name != "" {
			deps = append(deps, name)
		}
	}
	return deps
}

// parsePythonDependencyName extracts the bare package name from a PEP 508 dependency string.
func parsePythonDependencyName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if idx := strings.Index(raw, ";"); idx >= 0 {
		raw = strings.TrimSpace(raw[:idx])
	}

	for _, sep := range []string{"==", ">=", "<=", "~=", "!=", ">", "<", "[", " "} {
		if idx := strings.Index(raw, sep); idx >= 0 {
			raw = strings.TrimSpace(raw[:idx])
		}
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return strings.ToLower(raw)
}
