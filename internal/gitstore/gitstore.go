// Package gitstore is the artifact plane (ADR-0002/0004): server-owned bare
// git repositories mutated exclusively by exec'ing the system git binary via
// short-lived local clones. Git is the source of truth; callers project its
// state into Postgres. Writes are serialized per repo.
package gitstore

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const (
	MaxFileBytes  = 1 << 20 // 1 MiB per file
	MaxFilesWrite = 200
)

var (
	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	// artifact names are slugs; branch user parts are slugs too.
)

type File struct {
	Path    string `json:"path"`    // relative to the artifact dir
	Content string `json:"content"` // full file content (text)
}

type TreeEntry struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

type Store struct {
	base  string
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func New(baseDir string) *Store {
	return &Store{base: baseDir, locks: map[string]*sync.Mutex{}}
}

func (s *Store) repoLock(path string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locks[path]
	if !ok {
		l = &sync.Mutex{}
		s.locks[path] = l
	}
	return l
}

func (s *Store) repoPath(org, project string) (string, error) {
	if !slugRe.MatchString(org) || !slugRe.MatchString(project) {
		return "", fmt.Errorf("invalid org/project slug")
	}
	return filepath.Join(s.base, "repos", org, project+".git"), nil
}

// git runs a git command with a hermetic environment (no user/system config,
// no hook templates) and returns combined output on error for diagnostics.
func (s *Store) git(ctx context.Context, dir string, args ...string) (string, error) {
	base := []string{
		"-c", "user.name=ubiqo", "-c", "user.email=fabric@ubiqo.local",
		"-c", "init.defaultBranch=main", "-c", "core.hooksPath=/dev/null",
		"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false",
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, truncate(string(out), 500))
	}
	return string(out), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// EnsureRepo creates the bare repo with an initial empty commit on main.
func (s *Store) EnsureRepo(ctx context.Context, org, project string) error {
	path, err := s.repoPath(org, project)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	lock := s.repoLock(path)
	lock.Lock()
	defer lock.Unlock()
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(path, 0o750); err != nil {
		return err
	}
	if _, err := s.git(ctx, "", "init", "--bare", "--template=", "-b", "main", path); err != nil {
		return err
	}
	tmp, cleanup, err := s.clone(ctx, path, "main", true)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := s.git(ctx, tmp, "commit", "--allow-empty", "-m", "ubiqo: initialize project"); err != nil {
		return err
	}
	_, err = s.git(ctx, tmp, "push", "origin", "HEAD:refs/heads/main")
	return err
}

// clone makes a fast local clone. If the branch doesn't exist yet it clones
// the default branch and creates it (fresh=false callers rely on this).
func (s *Store) clone(ctx context.Context, repoPath, branch string, allowEmpty bool) (dir string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "ubiqo-git-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }
	args := []string{"clone", "--quiet", "--no-hardlinks"}
	if !allowEmpty {
		args = append(args, "--branch", branch)
	}
	args = append(args, repoPath, tmp)
	if _, err := s.git(ctx, "", args...); err != nil {
		// branch may not exist yet: clone default and create it
		if allowEmpty {
			cleanup()
			return "", nil, err
		}
		if _, err2 := s.git(ctx, "", "clone", "--quiet", "--no-hardlinks", repoPath, tmp); err2 != nil {
			cleanup()
			return "", nil, err2
		}
		if _, err2 := s.git(ctx, tmp, "checkout", "-B", branch); err2 != nil {
			cleanup()
			return "", nil, err2
		}
	}
	return tmp, cleanup, nil
}

// validRelPath enforces the git-plane hardening rules (security review):
// clean relative paths only, no traversal, no .git components, sane charset.
func validRelPath(p string) error {
	if p == "" || len(p) > 300 {
		return fmt.Errorf("invalid path %q", p)
	}
	if strings.Contains(p, "\\") || strings.ContainsAny(p, "\x00\n\r") {
		return fmt.Errorf("invalid characters in path %q", p)
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	if clean != p || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") {
		return fmt.Errorf("path %q must be a clean relative path", p)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".git" || part == ".." || part == "" {
			return fmt.Errorf("path %q contains a forbidden component", p)
		}
	}
	return nil
}

// ProposalBranch is the canonical branch name for a user's work on an artifact.
func ProposalBranch(username, artifact string) string {
	return "u/" + username + "/" + artifact
}

// WriteFiles commits files under shared/<artifact>/ onto branch (creating it
// from main if needed) and returns the commit SHA. Trailers are appended to
// the commit message (provenance, §5.2).
func (s *Store) WriteFiles(ctx context.Context, org, project, branch, artifact string, files []File, message string, trailers map[string]string) (string, error) {
	path, err := s.repoPath(org, project)
	if err != nil {
		return "", err
	}
	if !slugRe.MatchString(artifact) {
		return "", fmt.Errorf("invalid artifact name %q (lowercase slug required)", artifact)
	}
	if len(files) == 0 || len(files) > MaxFilesWrite {
		return "", fmt.Errorf("between 1 and %d files per write", MaxFilesWrite)
	}
	for _, f := range files {
		if err := validRelPath(f.Path); err != nil {
			return "", err
		}
		if len(f.Content) > MaxFileBytes {
			return "", fmt.Errorf("file %q exceeds %d bytes", f.Path, MaxFileBytes)
		}
	}
	lock := s.repoLock(path)
	lock.Lock()
	defer lock.Unlock()

	tmp, cleanup, err := s.clone(ctx, path, branch, false)
	if err != nil {
		return "", err
	}
	defer cleanup()

	dir := filepath.Join(tmp, "shared", artifact)
	for _, f := range files {
		full := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			return "", err
		}
		// Regular files only — never symlinks (and refuse to follow one).
		if fi, err := os.Lstat(full); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to write through symlink %q", f.Path)
		}
		if err := os.WriteFile(full, []byte(f.Content), 0o640); err != nil {
			return "", err
		}
	}
	if _, err := s.git(ctx, tmp, "add", "-A", "--", filepath.Join("shared", artifact)); err != nil {
		return "", err
	}
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "update " + artifact
	}
	msg += "\n\n" + formatTrailers(trailers)
	if _, err := s.git(ctx, tmp, "commit", "--allow-empty", "-m", msg); err != nil {
		return "", err
	}
	sha, err := s.git(ctx, tmp, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if _, err := s.git(ctx, tmp, "push", "origin", "HEAD:refs/heads/"+branch); err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}

func formatTrailers(trailers map[string]string) string {
	keys := make([]string, 0, len(trailers))
	for k := range trailers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := strings.ReplaceAll(trailers[k], "\n", " ")
		fmt.Fprintf(&b, "%s: %s\n", k, v)
	}
	return b.String()
}

// MergeResult reports either a merged SHA or the conflicting files.
type MergeResult struct {
	SHA       string
	Conflicts []string
}

// Merge merges branch into main (no-ff). On conflict it aborts cleanly and
// returns the conflicting paths; main is never left dirty.
func (s *Store) Merge(ctx context.Context, org, project, branch, message string, trailers map[string]string) (*MergeResult, error) {
	path, err := s.repoPath(org, project)
	if err != nil {
		return nil, err
	}
	lock := s.repoLock(path)
	lock.Lock()
	defer lock.Unlock()

	tmp, cleanup, err := s.clone(ctx, path, "main", false)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	msg := strings.TrimSpace(message) + "\n\n" + formatTrailers(trailers)
	if _, err := s.git(ctx, tmp, "merge", "--no-ff", "-m", msg, "origin/"+branch); err != nil {
		// collect conflicts, then abort
		out, _ := s.git(ctx, tmp, "diff", "--name-only", "--diff-filter=U")
		conflicts := splitLines(out)
		_, _ = s.git(ctx, tmp, "merge", "--abort")
		if len(conflicts) > 0 {
			return &MergeResult{Conflicts: conflicts}, nil
		}
		return nil, err
	}
	sha, err := s.git(ctx, tmp, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	if _, err := s.git(ctx, tmp, "push", "origin", "HEAD:refs/heads/main"); err != nil {
		return nil, err
	}
	return &MergeResult{SHA: strings.TrimSpace(sha)}, nil
}

// DeleteBranch removes a proposal branch after merge/reject.
func (s *Store) DeleteBranch(ctx context.Context, org, project, branch string) error {
	path, err := s.repoPath(org, project)
	if err != nil {
		return err
	}
	lock := s.repoLock(path)
	lock.Lock()
	defer lock.Unlock()
	_, err = s.git(ctx, path, "branch", "-D", branch)
	return err
}

// ReadFile returns a file at ref (branch, tag, or SHA).
func (s *Store) ReadFile(ctx context.Context, org, project, ref, artifact, relPath string) (string, error) {
	path, err := s.repoPath(org, project)
	if err != nil {
		return "", err
	}
	if err := validRelPath(relPath); err != nil {
		return "", err
	}
	if err := validRef(ref); err != nil {
		return "", err
	}
	out, err := s.git(ctx, path, "show", ref+":shared/"+artifact+"/"+relPath)
	if err != nil {
		return "", fmt.Errorf("not found: %s/%s at %s", artifact, relPath, ref)
	}
	return out, nil
}

// ListTree lists files under an artifact at ref.
func (s *Store) ListTree(ctx context.Context, org, project, ref, artifact string) ([]TreeEntry, error) {
	path, err := s.repoPath(org, project)
	if err != nil {
		return nil, err
	}
	if err := validRef(ref); err != nil {
		return nil, err
	}
	out, err := s.git(ctx, path, "ls-tree", "-r", "--long", ref, "--", "shared/"+artifact)
	if err != nil {
		return nil, err
	}
	var entries []TreeEntry
	for _, line := range splitLines(out) {
		// <mode> <type> <sha> <size>\t<path>
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		meta := strings.Fields(line[:tab])
		if len(meta) < 4 || meta[1] != "blob" {
			continue
		}
		var size int64
		fmt.Sscanf(meta[3], "%d", &size)
		entries = append(entries, TreeEntry{
			Path:  strings.TrimPrefix(line[tab+1:], "shared/"+artifact+"/"),
			Bytes: size,
		})
	}
	return entries, nil
}

// ListArtifacts returns the top-level artifact dirs under shared/ on main.
func (s *Store) ListArtifacts(ctx context.Context, org, project string) ([]string, error) {
	path, err := s.repoPath(org, project)
	if err != nil {
		return nil, err
	}
	out, err := s.git(ctx, path, "ls-tree", "--name-only", "main", "--", "shared/")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range splitLines(out) {
		names = append(names, strings.TrimPrefix(line, "shared/"))
	}
	sort.Strings(names)
	return names, nil
}

// Tag creates the annotated release tag artifacts/<artifact>/<version> on main.
func (s *Store) Tag(ctx context.Context, org, project, artifact, version, notes string) (string, error) {
	path, err := s.repoPath(org, project)
	if err != nil {
		return "", err
	}
	lock := s.repoLock(path)
	lock.Lock()
	defer lock.Unlock()
	tag := "artifacts/" + artifact + "/" + version
	if notes == "" {
		notes = "release " + artifact + " " + version
	}
	if _, err := s.git(ctx, path, "tag", "-a", tag, "-m", notes, "main"); err != nil {
		return "", err
	}
	return tag, nil
}

// Versions lists released version strings (vX.Y.Z) for an artifact.
func (s *Store) Versions(ctx context.Context, org, project, artifact string) ([]string, error) {
	path, err := s.repoPath(org, project)
	if err != nil {
		return nil, err
	}
	out, err := s.git(ctx, path, "tag", "-l", "artifacts/"+artifact+"/*")
	if err != nil {
		return nil, err
	}
	var versions []string
	for _, t := range splitLines(out) {
		versions = append(versions, strings.TrimPrefix(t, "artifacts/"+artifact+"/"))
	}
	return versions, nil
}

// DiffStat summarizes branch vs main for proposal review (capped).
func (s *Store) DiffStat(ctx context.Context, org, project, branch string) (string, error) {
	path, err := s.repoPath(org, project)
	if err != nil {
		return "", err
	}
	stat, err := s.git(ctx, path, "diff", "--stat=100", "main...refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	patch, err := s.git(ctx, path, "diff", "main...refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	if len(patch) > 12000 {
		patch = patch[:12000] + "\n… (diff truncated; use ubiqo_read_artifact with ref for full files)"
	}
	return stat + "\n" + patch, nil
}

// Branches lists proposal branches (u/*).
func (s *Store) Branches(ctx context.Context, org, project string) ([]string, error) {
	path, err := s.repoPath(org, project)
	if err != nil {
		return nil, err
	}
	out, err := s.git(ctx, path, "for-each-ref", "--format=%(refname:short)", "refs/heads/u/")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// BundleAll writes `git bundle` files for every repo into destDir and
// returns their relative names (used by `ubiqo backup`).
func (s *Store) BundleAll(ctx context.Context, destDir string) ([]string, error) {
	root := filepath.Join(s.base, "repos")
	var bundles []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || !strings.HasSuffix(p, ".git") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		name := strings.ReplaceAll(strings.TrimSuffix(rel, ".git"), string(filepath.Separator), "__") + ".bundle"
		out := filepath.Join(destDir, name)
		if _, gerr := s.git(ctx, p, "bundle", "create", out, "--all"); gerr != nil {
			return gerr
		}
		bundles = append(bundles, name)
		return filepath.SkipDir
	})
	return bundles, err
}

func validRef(ref string) error {
	if ref == "" || len(ref) > 200 || strings.HasPrefix(ref, "-") ||
		strings.ContainsAny(ref, " \t\n\r~^:?*[\\") || strings.Contains(ref, "..") {
		return fmt.Errorf("invalid ref %q", ref)
	}
	return nil
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// HealthCheck verifies the data dir is writable (readyz).
func (s *Store) HealthCheck() error {
	dir := filepath.Join(s.base, "repos")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

