package main

// Project readiness detection for sequential launchpad launches.
//
// Two strategies are tried concurrently for each project:
//  1. Port detection  – polls localhost:<port> via TCP dial.  The port is
//     first inferred from the framework's convention; any port number
//     announced in the process output is also polled dynamically.
//  2. Text detection  – scans the process stdout+stderr for per-framework
//     "server ready" log patterns (case-insensitive substring match).
//
// The project command is executed as a detached background process with its
// combined output piped to the caller.  Once readiness is confirmed (or the
// timeout expires) the child is released and continues running independently
// of the lo process.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// defaultReadyTimeout is the maximum time to wait for a project to signal
// readiness before proceeding to the next sequential batch anyway.
const defaultReadyTimeout = 60 * time.Second

// readySignal carries the outcome of waitForProjectReady.
type readySignal struct {
	How  string        // "port", "text", or "timeout"
	Port int           // detected port (port strategy only)
	Line string        // matched log line (text strategy only)
	Took time.Duration // elapsed time since launch
}

// frameworkReadyPatterns holds per-framework substrings that indicate the dev
// server has finished starting.  All comparisons are lower-cased.
var frameworkReadyPatterns = map[string][]string{
	// ---- Node.js frameworks ----
	"next":       {"✓ ready", "ready started server", "ready in", "started server on"},
	"nuxt":       {"nitro server started", "listening on", "✓ nitro"},
	"vite":       {"ready in", "local:", "➜  local:"},
	"vite+react": {"ready in", "local:", "➜  local:"},
	"vite+vue":   {"ready in", "local:", "➜  local:"},
	"sveltekit":  {"server listening on", "listening on"},
	"astro":      {"server listening on", "watching for file changes"},
	"remix":      {"remix app server started", "💿"},
	"nestjs":     {"nest application successfully started", "application is running on"},
	"express":    {"listening on", "server started", "app listening"},
	"fastify":    {"server listening at", "fastify listening"},
	"hono":       {"server is running", "listening on"},
	"angular":    {"compiled successfully", "webpack compiled", "live development server"},
	"react":      {"ready in", "local:"},
	"vue":        {"ready in", "local:"},
	"node":       {"listening on", "server started", "ready"},
	// ---- Go frameworks ----
	"gin":      {"listening and serving", "running on"},
	"fiber":    {"listen", "fiber"},
	"echo":     {"http server started on"},
	"chi":      {"listening on", "server started"},
	"temporal": {"server started"},
	"go":       {"listening on", "server started"},
	// ---- Rust frameworks ----
	"axum":   {"listening on"},
	"actix":  {"started", "actix"},
	"rocket": {"rocket has launched", "launching"},
	"cargo":  {"listening on"},
	// ---- Python frameworks ----
	"fastapi":   {"uvicorn running on", "application startup complete"},
	"flask":     {"running on http", "serving flask app"},
	"django":    {"starting development server at", "quit the server"},
	"streamlit": {"you can now view", "local url:"},
	"gradio":    {"running on local url", "running on public url"},
	"python":    {"started", "running on"},
	// ---- Java frameworks ----
	"spring":    {"started", "tomcat started on port", "started in"},
	"quarkus":   {"started in", "listening on"},
	"micronaut": {"server running:", "startup completed"},
	"java":      {"started", "listening"},
}

// frameworkDefaultPorts maps frameworks to their conventional dev server ports.
var frameworkDefaultPorts = map[string]int{
	// Node
	"next":       3000,
	"nuxt":       3000,
	"vite":       5173,
	"vite+react": 5173,
	"vite+vue":   5173,
	"sveltekit":  5173,
	"astro":      4321,
	"remix":      3000,
	"nestjs":     3000,
	"express":    3000,
	"fastify":    3000,
	"hono":       3000,
	"angular":    4200,
	"react":      5173,
	"vue":        5173,
	// Go
	"gin":   8080,
	"fiber": 8080,
	"echo":  8080,
	"chi":   8080,
	// Rust
	"axum":   3000,
	"actix":  8080,
	"rocket": 8000,
	// Python
	"fastapi":   8000,
	"flask":     5000,
	"django":    8000,
	"streamlit": 8501,
	"gradio":    7860,
	// Java
	"spring":    8080,
	"quarkus":   8080,
	"micronaut": 8080,
}

// portRE matches common port-announcement patterns in framework log output.
// Group 1: host:port style  (localhost:3000, 0.0.0.0:8080, :::8080)
// Group 2: "port N" style   (port 3000, port=8080, Port: 8080)
var portRE = regexp.MustCompile(
	`(?:localhost|0\.0\.0\.0|127\.0\.0\.1|:::?)[:=](\d{2,5})` +
		`|` +
		`(?i:port)[=: ]+(\d{2,5})`,
)

// extractPortFromLine attempts to parse a valid port number (≥1024) from a
// single log line.  Returns 0 if nothing useful is found.
func extractPortFromLine(line string) int {
	m := portRE.FindStringSubmatch(line)
	if m == nil {
		return 0
	}
	for _, s := range m[1:] {
		if s == "" {
			continue
		}
		p, err := strconv.Atoi(s)
		if err == nil && p >= 1024 && p <= 65535 {
			return p
		}
	}
	return 0
}

// detectFramework extracts the short framework name (e.g. "vite+react", "gin")
// from the project at projectPath using the existing stack preview function.
func detectFramework(projectPath string) string {
	preview := previewProjectStack(projectPath)
	// preview is e.g. " node / vite+react" or "🐹 go / gin"
	parts := strings.SplitN(preview, " / ", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// pollPort repeatedly tries a non-blocking TCP connect to 127.0.0.1:port
// every 500 ms until the context is cancelled or a connection succeeds.
func pollPort(ctx context.Context, port int) bool {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// startPortPoller launches a goroutine that polls port and sends on resultCh
// when the port accepts a connection.
func startPortPoller(ctx context.Context, port int, startedAt time.Time, resultCh chan<- readySignal) {
	go func() {
		if pollPort(ctx, port) {
			select {
			case resultCh <- readySignal{How: "port", Port: port, Took: time.Since(startedAt)}:
			default:
			}
		}
	}()
}

// waitForProjectReady launches the project in a visible terminal window while
// simultaneously writing its output to a temp log file via tee.  Lo then tails
// the log, waiting for port/text readiness signals.
//
// The terminal window stays open after detection so the user always has a
// visible, interactive shell for the project.
func waitForProjectReady(project projectEntry, timeout time.Duration, out io.Writer) (readySignal, error) {
	target, installCmd, runCmd, err := detectProjectRunner(project.Path)
	if err != nil {
		return readySignal{}, err
	}

	// Run dependency installation synchronously before starting the server.
	if len(installCmd) > 0 {
		fmt.Fprintf(out, "  📦 %s: installing dependencies...\n", project.Name)
		if err := runCommandInDir(project.Path, installCmd, out, out); err != nil {
			return readySignal{}, fmt.Errorf("install: %w", err)
		}
	}

	fmt.Fprintf(out, "  🚀 %s → %s\n", project.Name, target)

	// Create a temp log file that tee will write into.
	logFile, err := os.CreateTemp("", fmt.Sprintf("liftoff-%s-*.log", project.Name))
	if err != nil {
		return readySignal{}, fmt.Errorf("tempfile: %w", err)
	}
	logPath := logFile.Name()
	logFile.Close() // tee will open/write it; we only need the path
	defer os.Remove(logPath)

	// Launch in a real terminal window: output goes to the window AND the log file.
	if err := launchInTerminalWithLog(project.Path, runCmd, logPath, out, out); err != nil {
		return readySignal{}, fmt.Errorf("launch %s: %w", project.Name, err)
	}

	// Wait briefly for the terminal/tee to create and start writing the log.
	// (The process starts asynchronously; the file may be empty for a moment.)
	for i := 0; i < 20; i++ {
		if info, err := os.Stat(logPath); err == nil && info.Size() > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Open a read handle for tailing.
	readFd, err := os.Open(logPath)
	if err != nil {
		return readySignal{}, fmt.Errorf("open log: %w", err)
	}
	defer readFd.Close()

	framework := detectFramework(project.Path)
	readyPatterns := frameworkReadyPatterns[framework]
	defaultPort := frameworkDefaultPorts[framework]

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Buffered so goroutines never block.
	resultCh := make(chan readySignal, 8)
	startedAt := time.Now()

	// Start port poller for the framework's conventional port.
	if defaultPort > 0 {
		startPortPoller(ctx, defaultPort, startedAt, resultCh)
	}

	// Tail the log file, looking for readiness text and dynamic port announcements.
	go func() {
		reader := bufio.NewReader(readFd)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				line = strings.TrimRight(line, "\r\n")
				fmt.Fprintf(out, "  %s │ %s\n", project.Name, line)

				// Dynamically extract port from this line and start polling.
				if p := extractPortFromLine(line); p > 0 && p != defaultPort {
					startPortPoller(ctx, p, startedAt, resultCh)
				}

				// Check text-based readiness patterns.
				lower := strings.ToLower(line)
				for _, pat := range readyPatterns {
					if strings.Contains(lower, pat) {
						select {
						case resultCh <- readySignal{How: "text", Line: line, Took: time.Since(startedAt)}:
						default:
						}
						return
					}
				}
			}

			if err != nil {
				// EOF means the child hasn't written more yet; poll and retry.
				select {
				case <-ctx.Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
			}
		}
	}()

	select {
	case sig := <-resultCh:
		cancel()
		return sig, nil
	case <-ctx.Done():
		return readySignal{How: "timeout", Took: timeout}, nil
	}
}
