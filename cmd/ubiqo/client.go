// Client-side commands: login/init/context/hook. Hooks are FAIL-OPEN
// (sysadmin review): 3s budget, cached last-good bundle on any failure, and a
// visible status line so a brainless session is never silent (UX review).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type clientConfig struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".ubiqo")
}

func loadClientConfig() (*clientConfig, error) {
	// Env wins (CI, containers); file otherwise.
	if s, t := os.Getenv("UBIQO_SERVER_URL"), os.Getenv("UBIQO_TOKEN"); s != "" && t != "" {
		return &clientConfig{Server: strings.TrimRight(s, "/"), Token: t}, nil
	}
	b, err := os.ReadFile(filepath.Join(configDir(), "config.json"))
	if err != nil {
		return nil, fmt.Errorf("not logged in: run `ubiqo login --server URL --token TOKEN` (or set UBIQO_SERVER_URL/UBIQO_TOKEN)")
	}
	var c clientConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	server := fs.String("server", "", "ubiqo server URL")
	token := fs.String("token", "", "device token (ubq_...)")
	_ = fs.Parse(args)
	if *server == "" || *token == "" {
		return fmt.Errorf("login requires --server and --token")
	}
	c := &clientConfig{Server: strings.TrimRight(*server, "/"), Token: *token}
	// Validate against /v1/meta before persisting.
	meta, err := httpGet(c, "/v1/meta", 5*time.Second)
	if err != nil {
		return fmt.Errorf("server unreachable: %w", err)
	}
	var m struct {
		ServerVersion string `json:"server_version"`
		MinCLI        string `json:"min_cli_version"`
	}
	_ = json.Unmarshal(meta, &m)
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(filepath.Join(configDir(), "config.json"), b, 0o600); err != nil {
		return err
	}
	fmt.Printf("logged in to %s (server %s)\n", c.Server, m.ServerVersion)
	fmt.Println("next: `ubiqo init --project <slug>` inside your project directory")
	return nil
}

type projectBinding struct {
	Project string `json:"project"`
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	project := fs.String("project", "", "ubiqo project slug")
	_ = fs.Parse(args)
	if *project == "" {
		return fmt.Errorf("init requires --project")
	}
	b, _ := json.MarshalIndent(projectBinding{Project: *project}, "", "  ")
	if err := os.WriteFile("ubiqo.yaml", append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("bound this directory to project %q (ubiqo.yaml)\n", *project)
	return nil
}

// boundProject resolves the project slug: flag > ubiqo.yaml (walking up) > env.
func boundProject(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if p := os.Getenv("UBIQO_PROJECT"); p != "" {
		return p, nil
	}
	dir, _ := os.Getwd()
	for i := 0; i < 10 && dir != "/" && dir != "."; i++ {
		if b, err := os.ReadFile(filepath.Join(dir, "ubiqo.yaml")); err == nil {
			var pb projectBinding
			if json.Unmarshal(b, &pb) == nil && pb.Project != "" {
				return pb.Project, nil
			}
		}
		dir = filepath.Dir(dir)
	}
	return "", fmt.Errorf("no project bound: run `ubiqo init --project <slug>` here or set UBIQO_PROJECT")
}

func cmdContext(args []string) error {
	if len(args) < 1 || args[0] != "pull" {
		return fmt.Errorf("usage: ubiqo context pull [--project SLUG]")
	}
	fs := flag.NewFlagSet("context pull", flag.ExitOnError)
	project := fs.String("project", "", "project slug (default: ubiqo.yaml binding)")
	_ = fs.Parse(args[1:])
	text, status, err := pullContext(*project, 15*time.Second)
	if err != nil {
		return err
	}
	fmt.Println(status)
	fmt.Println(text)
	return nil
}

// pullContext fetches /v1/context with caching. Returns (body, statusLine).
func pullContext(project string, timeout time.Duration) (string, string, error) {
	slug, err := boundProject(project)
	if err != nil {
		return "", "", err
	}
	cachePath := filepath.Join(configDir(), "cache", slug+".ctx")
	c, err := loadClientConfig()
	if err == nil {
		body, version, ferr := fetchContext(c, slug, timeout)
		if ferr == nil {
			_ = os.MkdirAll(filepath.Dir(cachePath), 0o700)
			_ = os.WriteFile(cachePath, []byte(body), 0o600)
			return body, "ubiqo: context " + version + " loaded (project " + slug + ")", nil
		}
		err = ferr
	}
	// fail open: cached bundle + loud status line
	if cached, cerr := os.ReadFile(cachePath); cerr == nil {
		return string(cached), fmt.Sprintf("ubiqo: OFFLINE (%v) — using STALE cached context for %s", err, slug), nil
	}
	return "", "", fmt.Errorf("ubiqo unreachable and no cached context for %q: %w", slug, err)
}

func fetchContext(c *clientConfig, project string, timeout time.Duration) (body, version string, err error) {
	req, _ := http.NewRequest(http.MethodGet, c.Server+"/v1/context?project="+project, nil)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("server said %d: %s", resp.StatusCode, firstN(string(b), 200))
	}
	return string(b), resp.Header.Get("X-Ubiqo-Bundle-Version"), nil
}

// ─── Claude Code hook entrypoints ───

// SessionStart: stdout becomes session context. Budget 3s, then cache.
// SessionEnd: report a session summary event; spool on failure and retry
// on the next invocation (fire-and-forget semantics for the user).
func cmdHook(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: ubiqo hook session-start|session-end")
	}
	switch args[0] {
	case "session-start":
		text, status, err := pullContext("", 3*time.Second)
		if err != nil {
			// Fail OPEN: a broken hook must never block the session.
			fmt.Printf("ubiqo: unavailable (%v) — session starts without shared context\n", err)
			return nil
		}
		fmt.Println(status)
		fmt.Println()
		fmt.Println(text)
		return nil
	case "session-end":
		slug, err := boundProject("")
		if err != nil {
			return nil // unbound dir: nothing to report
		}
		summary := os.Getenv("UBIQO_SESSION_SUMMARY")
		if summary == "" {
			host, _ := os.Hostname()
			summary = "session ended on " + host
		}
		drainSpool()
		if err := postSessionEnd(slug, summary, 3*time.Second); err != nil {
			spool(slug, summary)
		}
		return nil
	}
	return fmt.Errorf("unknown hook %q", args[0])
}

func postSessionEnd(project, summary string, timeout time.Duration) error {
	c, err := loadClientConfig()
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"project": project, "summary": summary})
	req, _ := http.NewRequest(http.MethodPost, c.Server+"/v1/session-end", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("server said %d", resp.StatusCode)
	}
	return nil
}

func spool(project, summary string) {
	dir := filepath.Join(configDir(), "spool")
	_ = os.MkdirAll(dir, 0o700)
	b, _ := json.Marshal(map[string]string{"project": project, "summary": summary})
	_ = os.WriteFile(filepath.Join(dir, uuid.NewString()+".json"), b, 0o600)
}

func drainSpool() {
	dir := filepath.Join(configDir(), "spool")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var m map[string]string
		if json.Unmarshal(b, &m) != nil {
			_ = os.Remove(path)
			continue
		}
		if postSessionEnd(m["project"], m["summary"], 2*time.Second) == nil {
			_ = os.Remove(path)
		}
	}
}

func httpGet(c *clientConfig, path string, timeout time.Duration) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, c.Server+path, nil)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s → %d", path, resp.StatusCode)
	}
	return b, nil
}

func firstN(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func readAll(r io.Reader) ([]byte, error) { return io.ReadAll(io.LimitReader(r, 4<<20)) }

func parseUUID(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, fmt.Errorf("--id required")
	}
	return uuid.Parse(s)
}
