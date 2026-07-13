package gitstore

import (
	"context"
	"strings"
	"testing"
)

func newStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s := New(t.TempDir())
	ctx := context.Background()
	if err := s.EnsureRepo(ctx, "acme", "web"); err != nil {
		t.Fatalf("ensure repo: %v", err)
	}
	return s, ctx
}

func TestWriteReadListRelease(t *testing.T) {
	s, ctx := newStore(t)
	sha, err := s.WriteFiles(ctx, "acme", "web", "main", "api-design",
		[]File{{Path: "README.md", Content: "# API design\nv1\n"}, {Path: "endpoints/list.md", Content: "GET /things\n"}},
		"initial API design", map[string]string{"Ubiqo-User": "jon", "Model": "claude"})
	if err != nil || len(sha) != 40 {
		t.Fatalf("write: %v sha=%q", err, sha)
	}
	got, err := s.ReadFile(ctx, "acme", "web", "main", "api-design", "README.md")
	if err != nil || !strings.Contains(got, "# API design") {
		t.Fatalf("read: %v %q", err, got)
	}
	arts, err := s.ListArtifacts(ctx, "acme", "web")
	if err != nil || len(arts) != 1 || arts[0] != "api-design" {
		t.Fatalf("list artifacts: %v %v", arts, err)
	}
	tree, err := s.ListTree(ctx, "acme", "web", "main", "api-design")
	if err != nil || len(tree) != 2 {
		t.Fatalf("tree: %v %v", tree, err)
	}
	// release tag + read at tag after main moves on
	if _, err := s.Tag(ctx, "acme", "web", "api-design", "v1.0.0", "first"); err != nil {
		t.Fatalf("tag: %v", err)
	}
	if _, err := s.WriteFiles(ctx, "acme", "web", "main", "api-design",
		[]File{{Path: "README.md", Content: "# API design\nv2 draft\n"}}, "draft v2", nil); err != nil {
		t.Fatal(err)
	}
	atTag, err := s.ReadFile(ctx, "acme", "web", "artifacts/api-design/v1.0.0", "api-design", "README.md")
	if err != nil || !strings.Contains(atTag, "v1") || strings.Contains(atTag, "v2") {
		t.Fatalf("read at tag must pin content: %v %q", err, atTag)
	}
	vs, err := s.Versions(ctx, "acme", "web", "api-design")
	if err != nil || len(vs) != 1 || vs[0] != "v1.0.0" {
		t.Fatalf("versions: %v %v", vs, err)
	}
}

func TestBranchProposeMergeClean(t *testing.T) {
	s, ctx := newStore(t)
	if _, err := s.WriteFiles(ctx, "acme", "web", "main", "doc",
		[]File{{Path: "a.md", Content: "base\n"}}, "base", nil); err != nil {
		t.Fatal(err)
	}
	br := ProposalBranch("jon", "doc")
	if _, err := s.WriteFiles(ctx, "acme", "web", br, "doc",
		[]File{{Path: "b.md", Content: "new file\n"}}, "add b", nil); err != nil {
		t.Fatal(err)
	}
	res, err := s.Merge(ctx, "acme", "web", br, "merge jon/doc", map[string]string{"Ubiqo-User": "sam"})
	if err != nil || res.SHA == "" || len(res.Conflicts) != 0 {
		t.Fatalf("clean merge failed: %+v %v", res, err)
	}
	if _, err := s.ReadFile(ctx, "acme", "web", "main", "doc", "b.md"); err != nil {
		t.Fatalf("merged file must exist on main: %v", err)
	}
}

// The conflict corpus (QA review): same-line, edit-vs-delete, and repo
// cleanliness after aborted merges.
func TestMergeConflictCorpus(t *testing.T) {
	s, ctx := newStore(t)
	write := func(branch, path, content, msg string) {
		t.Helper()
		if _, err := s.WriteFiles(ctx, "acme", "web", branch, "doc",
			[]File{{Path: path, Content: content}}, msg, nil); err != nil {
			t.Fatal(err)
		}
	}
	write("main", "spec.md", "line1\nline2\nline3\n", "base")

	// same-line conflict
	br := ProposalBranch("jon", "doc")
	write(br, "spec.md", "line1\nJON WAS HERE\nline3\n", "jon edit")
	write("main", "spec.md", "line1\nSAM WAS HERE\nline3\n", "sam edit")
	res, err := s.Merge(ctx, "acme", "web", br, "try merge", nil)
	if err != nil {
		t.Fatalf("conflict should not be an error: %v", err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0] != "shared/doc/spec.md" {
		t.Fatalf("want conflict on shared/doc/spec.md, got %v", res.Conflicts)
	}
	// main must be clean: still readable and mergeable afterwards
	got, err := s.ReadFile(ctx, "acme", "web", "main", "doc", "spec.md")
	if err != nil || !strings.Contains(got, "SAM WAS HERE") {
		t.Fatalf("main dirty after aborted merge: %v %q", err, got)
	}

	// adjacent-line edits: git resolves these — expect clean merge
	br2 := ProposalBranch("ana", "doc")
	write(br2, "spec.md", "line1\nSAM WAS HERE\nline3 edited by ana\n", "ana edit line3")
	res2, err := s.Merge(ctx, "acme", "web", br2, "merge ana", nil)
	if err != nil || len(res2.Conflicts) != 0 {
		t.Fatalf("adjacent-line merge should be clean: %+v %v", res2, err)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	s, ctx := newStore(t)
	bad := []string{"../evil.md", "a/../../evil.md", "/abs.md", ".git/hooks/pre-receive", "a//b.md", "nul\x00.md"}
	for _, p := range bad {
		if _, err := s.WriteFiles(ctx, "acme", "web", "main", "doc",
			[]File{{Path: p, Content: "x"}}, "evil", nil); err == nil {
			t.Errorf("path %q must be rejected", p)
		}
	}
	if _, err := s.WriteFiles(ctx, "acme", "web", "main", "Bad Name",
		[]File{{Path: "ok.md", Content: "x"}}, "evil", nil); err == nil {
		t.Error("invalid artifact name must be rejected")
	}
	if _, err := s.ReadFile(ctx, "acme", "web", "--output=/tmp/pwn", "doc", "a.md"); err == nil {
		t.Error("flag-like ref must be rejected")
	}
}
