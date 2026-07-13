package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jongodb/ubiqo/internal/authz"
	"github.com/jongodb/ubiqo/internal/core"
	"github.com/jongodb/ubiqo/internal/gitstore"
	"github.com/jongodb/ubiqo/internal/store"
)

// Every registered tool must declare an action (deny-by-default registry).
func TestRegistryDeclaresActions(t *testing.T) {
	svc := &core.Service{Version: "test"}
	_ = New(svc)
	if len(Registry) < 10 {
		t.Fatalf("expected the full tool surface registered, got %d", len(Registry))
	}
	for name, action := range Registry {
		if action == "" {
			t.Errorf("tool %s declares no action", name)
		}
		if !strings.HasPrefix(name, "ubiqo_") {
			t.Errorf("tool %s must carry the ubiqo_ prefix (collision-avoidance)", name)
		}
	}
}

func testDB(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("UBIQO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set UBIQO_TEST_DATABASE_URL to run integration tests")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.ResetForTest(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

type fixture struct {
	svc      *core.Service
	jon, sam *core.Actor
	call     func(t *testing.T, as *core.Actor, tool string, args map[string]any) (string, bool)
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db := testDB(t)
	git := gitstore.New(t.TempDir())
	svc := &core.Service{DB: db, Git: git, Version: "test"}

	org, err := db.CreateOrg(ctx, "acme", "Acme")
	if err != nil {
		t.Fatal(err)
	}
	jonU, _ := db.CreateUser(ctx, "jon", "Jon")
	samU, _ := db.CreateUser(ctx, "sam", "Sam")
	if err := db.AddOrgMember(ctx, org.ID, jonU.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddOrgMember(ctx, org.ID, samU.ID, "member"); err != nil {
		t.Fatal(err)
	}
	p, err := db.CreateProject(ctx, org.ID, "website-redesign", "Website Redesign", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddProjectMember(ctx, p, jonU.ID, "contributor"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddProjectMember(ctx, p, samU.ID, "maintainer"); err != nil {
		t.Fatal(err)
	}
	if err := git.EnsureRepo(ctx, "acme", "website-redesign"); err != nil {
		t.Fatal(err)
	}

	jon := &core.Actor{UserID: jonU.ID, Username: "jon", OrgID: org.ID, OrgSlug: "acme", OrgRole: "member", Identity: "claude-desktop/laptop-1"}
	sam := &core.Actor{UserID: samU.ID, Username: "sam", OrgID: org.ID, OrgSlug: "acme", OrgRole: "member", Identity: "claude-code/studio"}

	// The e2e runs over the real MCP protocol via in-memory transports; the
	// server-side middleware injects whichever actor the test selects
	// (standing in for the HTTP bearer middleware).
	var current atomic.Pointer[core.Actor]
	server := New(svc)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if a := current.Load(); a != nil {
				ctx = core.WithActor(ctx, a)
			}
			return next(ctx, method, req)
		}
	})
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	call := func(t *testing.T, as *core.Actor, tool string, args map[string]any) (string, bool) {
		t.Helper()
		current.Store(as)
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("protocol error calling %s: %v", tool, err)
		}
		var text string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text += tc.Text
			}
		}
		return text, res.IsError
	}
	return &fixture{svc: svc, jon: jon, sam: sam, call: call}
}

// The "day at Acme" over the wire: write → denial → propose → review →
// merge → release → pinned read → memory → activity.
func TestAcmeFlow(t *testing.T) {
	f := setup(t)
	proj := map[string]any{"project": "website-redesign"}
	arg := func(extra map[string]any) map[string]any {
		m := map[string]any{"project": "website-redesign"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	// whoami
	out, isErr := f.call(t, f.jon, "ubiqo_whoami", nil)
	if isErr || !strings.Contains(out, `"website-redesign":"contributor"`) {
		t.Fatalf("whoami: err=%v out=%s", isErr, out)
	}

	// jon writes → lands on his branch, not main
	out, isErr = f.call(t, f.jon, "ubiqo_write_artifact", arg(map[string]any{
		"artifact": "api-design", "message": "draft API",
		"files": []map[string]string{{"path": "README.md", "content": "# API\nREST under /v1\n"}},
	}))
	if isErr {
		t.Fatalf("write: %s", out)
	}
	var write core.WriteResult
	mustJSON(t, out, &write)
	if write.WroteToMain || write.Branch != "u/jon/api-design" {
		t.Fatalf("team mode must write to the proposer branch: %+v", write)
	}

	// jon tries to merge → structured denial teaching propose_merge
	out, isErr = f.call(t, f.jon, "ubiqo_merge_proposal", arg(map[string]any{"proposal_id": "00000000-0000-0000-0000-000000000000"}))
	if !isErr {
		t.Fatal("contributor merge must be denied")
	}
	var denial authz.Denial
	mustJSON(t, out, &denial)
	if denial.CallerRole != "contributor" || !strings.Contains(denial.Alternative, "ubiqo_propose_merge") {
		t.Fatalf("denial must teach the protocol: %s", out)
	}

	// jon proposes
	out, isErr = f.call(t, f.jon, "ubiqo_propose_merge", arg(map[string]any{"artifact": "api-design", "summary": "first draft"}))
	if isErr {
		t.Fatalf("propose: %s", out)
	}
	var prop core.ProposalResult
	mustJSON(t, out, &prop)

	// sam reviews with diff
	out, isErr = f.call(t, f.sam, "ubiqo_list_proposals", arg(map[string]any{"with_diff": true}))
	if isErr || !strings.Contains(out, "first draft") || !strings.Contains(out, "README.md") {
		t.Fatalf("list_proposals: err=%v out=%s", isErr, out)
	}

	// sam merges, then releases
	out, isErr = f.call(t, f.sam, "ubiqo_merge_proposal", arg(map[string]any{"proposal_id": prop.ProposalID}))
	if isErr || !strings.Contains(out, `"merged"`) {
		t.Fatalf("merge: err=%v out=%s", isErr, out)
	}
	out, isErr = f.call(t, f.sam, "ubiqo_release_artifact", arg(map[string]any{"artifact": "api-design", "bump": "minor", "notes": "first cut"}))
	if isErr {
		t.Fatalf("release: %s", out)
	}
	var rel core.ReleaseResult
	mustJSON(t, out, &rel)
	if rel.Version != "v0.1.0" || rel.Tag != "artifacts/api-design/v0.1.0" {
		t.Fatalf("release: %+v", rel)
	}

	// jon reads pinned at the tag; content must be framed as untrusted data
	out, isErr = f.call(t, f.jon, "ubiqo_read_artifact", arg(map[string]any{
		"artifact": "api-design", "ref": rel.Tag, "path": "README.md"}))
	if isErr || !strings.Contains(out, "REST under /v1") || !strings.Contains(out, "untrusted-data") {
		t.Fatalf("pinned read: err=%v out=%s", isErr, out)
	}

	// memory: jon stores project-scoped, sam recalls it
	if out, isErr = f.call(t, f.jon, "ubiqo_remember", arg(map[string]any{
		"scope": "project", "content": "Client prefers Tailwind v4 for all frontend work."})); isErr {
		t.Fatalf("remember: %s", out)
	}
	out, isErr = f.call(t, f.sam, "ubiqo_recall", arg(map[string]any{"query": "tailwind"}))
	if isErr || !strings.Contains(out, "Tailwind v4") {
		t.Fatalf("cross-account recall failed: err=%v out=%s", isErr, out)
	}
	// jon's PRIVATE memory must not reach sam
	if out, isErr = f.call(t, f.jon, "ubiqo_remember", arg(map[string]any{
		"scope": "user", "content": "My private draft password idea: hunter2"})); isErr {
		t.Fatalf("remember user: %s", out)
	}
	out, _ = f.call(t, f.sam, "ubiqo_recall", arg(map[string]any{"query": "hunter2"}))
	if strings.Contains(out, "hunter2") {
		t.Fatal("PRIVACY LEAK: sam recalled jon's user-scoped memory")
	}

	// activity narrates the whole story for account B
	out, isErr = f.call(t, f.sam, "ubiqo_get_activity", proj)
	if isErr || !strings.Contains(out, "release.published") || !strings.Contains(out, "proposal.merged") ||
		!strings.Contains(out, "jon via claude-desktop/laptop-1") {
		t.Fatalf("activity digest: err=%v out=%s", isErr, out)
	}

	// context bundle: identity header + protocol + digest, all present
	out, isErr = f.call(t, f.jon, "ubiqo_get_context", proj)
	if isErr {
		t.Fatalf("get_context: %s", out)
	}
	for _, want := range []string{"ubiqo user `jon`", "role `contributor`", "ubiqo_propose_merge", "Recent team activity", "untrusted-data"} {
		if !strings.Contains(out, want) {
			t.Errorf("get_context missing %q", want)
		}
	}
}

// Role-by-tool negative matrix over the real protocol (QA review).
func TestRBACMatrixOverMCP(t *testing.T) {
	f := setup(t)
	deniedForContributor := map[string]map[string]any{
		"ubiqo_merge_proposal": {"project": "website-redesign",
			"proposal_id": "00000000-0000-0000-0000-000000000000"},
		"ubiqo_release_artifact": {"project": "website-redesign",
			"artifact": "x", "bump": "minor"},
	}
	for tool, args := range deniedForContributor {
		out, isErr := f.call(t, f.jon, tool, args)
		if !isErr || !strings.Contains(out, "caller_role") {
			t.Errorf("%s must return a structured denial for contributor, got err=%v %s", tool, isErr, out)
		}
	}
	// Unauthenticated: no actor in ctx → every tool refuses
	out, isErr := f.call(t, nil, "ubiqo_get_context", map[string]any{"project": "website-redesign"})
	if !isErr || !strings.Contains(out, "unauthenticated") {
		t.Fatalf("unauthenticated call must be refused: err=%v %s", isErr, out)
	}
}

func mustJSON(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("expected JSON, got %q: %v", s, err)
	}
}
