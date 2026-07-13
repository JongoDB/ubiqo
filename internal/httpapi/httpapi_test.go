package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jongodb/ubiqo/internal/core"
	"github.com/jongodb/ubiqo/internal/gitstore"
	"github.com/jongodb/ubiqo/internal/store"
)

// The auth middleware is a security boundary: no token, garbage token, and
// revoked token must all be rejected; a real token must reach the handler.
func TestAuthAndHooks(t *testing.T) {
	url := os.Getenv("UBIQO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set UBIQO_TEST_DATABASE_URL to run integration tests")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.ResetForTest(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	git := gitstore.New(t.TempDir())
	svc := &core.Service{DB: db, Git: git, Version: "test"}
	api := &Server{Svc: svc, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Public: "http://x"}
	ts := httptest.NewServer(api.Handler())
	t.Cleanup(ts.Close)

	org, _ := db.CreateOrg(ctx, "acme", "Acme")
	u, _ := db.CreateUser(ctx, "jon", "")
	_ = db.AddOrgMember(ctx, org.ID, u.ID, "owner")
	p, _ := db.CreateProject(ctx, org.ID, "scratch", "Scratch", true)
	_ = db.AddProjectMember(ctx, p, u.ID, "maintainer")
	if err := git.EnsureRepo(ctx, "acme", "scratch"); err != nil {
		t.Fatal(err)
	}
	token, err := db.NewToken(ctx, org.ID, u.ID, "laptop", "test")
	if err != nil {
		t.Fatal(err)
	}

	get := func(path, bearer string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, _ := get("/healthz", ""); code != 200 {
		t.Fatalf("healthz: %d", code)
	}
	if code, _ := get("/readyz", ""); code != 200 {
		t.Fatalf("readyz: %d", code)
	}
	if code, body := get("/v1/meta", ""); code != 200 || !strings.Contains(body, "min_cli_version") {
		t.Fatalf("meta: %d %s", code, body)
	}
	// auth boundary
	if code, _ := get("/v1/context?project=scratch", ""); code != 401 {
		t.Fatalf("no token must be 401, got %d", code)
	}
	if code, _ := get("/v1/context?project=scratch", "ubq_deadbeef"); code != 401 {
		t.Fatalf("bad token must be 401, got %d", code)
	}
	code, body := get("/v1/context?project=scratch", token)
	if code != 200 || !strings.Contains(body, "SOLO mode") || !strings.Contains(body, "ubiqo user `jon`") {
		t.Fatalf("real token must serve the bundle: %d %s", code, body[:min(200, len(body))])
	}
	// revocation propagates
	tokens, _ := db.ListTokens(ctx, org.ID)
	id, _ := parseUUIDStr(tokens[0]["id"])
	if err := db.RevokeToken(ctx, id); err != nil {
		t.Fatal(err)
	}
	if code, _ := get("/v1/context?project=scratch", token); code != 401 {
		t.Fatalf("revoked token must be 401, got %d", code)
	}
}

func parseUUIDStr(s string) (uuid.UUID, error) { return uuid.Parse(s) }
