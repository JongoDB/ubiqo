// Package core is the service façade every interface (MCP, REST hooks, CLI)
// calls. It owns actor resolution, the RBAC gate (ADR-0005), the git-first
// mutation order (ADR-0004), and untrusted-data framing (ADR-0008). MCP tools
// stay thin adapters over this package (QA review: the test seam).
package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jongodb/ubiqo/internal/authz"
	"github.com/jongodb/ubiqo/internal/compiler"
	"github.com/jongodb/ubiqo/internal/gitstore"
	"github.com/jongodb/ubiqo/internal/semver"
	"github.com/jongodb/ubiqo/internal/store"
)

// Actor is the authenticated caller (resolved from a device token).
type Actor struct {
	UserID   uuid.UUID
	Username string
	OrgID    uuid.UUID
	OrgSlug  string
	OrgRole  authz.OrgRole
	Identity string // device label, e.g. "claude-code/laptop-1"
}

type ctxKey struct{}

func WithActor(ctx context.Context, a *Actor) context.Context {
	return context.WithValue(ctx, ctxKey{}, a)
}

func ActorFrom(ctx context.Context) (*Actor, bool) {
	a, ok := ctx.Value(ctxKey{}).(*Actor)
	return a, ok
}

type Service struct {
	DB      *store.Store
	Git     *gitstore.Store
	Version string // server version for /v1/meta and trailers
}

var ErrNotFound = store.ErrNotFound

// projectCtx resolves slug → project + the actor's role, applying the org
// fallback: org owners/admins get maintainer on every project (keeps solo
// and small-team setups zero-ceremony).
func (s *Service) projectCtx(ctx context.Context, a *Actor, slug string) (*store.Project, authz.Role, error) {
	p, err := s.DB.GetProject(ctx, a.OrgID, slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, authz.RoleNone, fmt.Errorf("project %q %w in org %q — ubiqo_whoami lists your projects", slug, store.ErrNotFound, a.OrgSlug)
		}
		return nil, authz.RoleNone, err
	}
	role, err := s.DB.ProjectRole(ctx, p.ID, a.UserID)
	if err != nil {
		return nil, authz.RoleNone, err
	}
	if role == authz.RoleNone && authz.OrgCanManage(a.OrgRole) {
		role = authz.RoleMaintainer
	}
	return p, role, nil
}

// gate authorizes one action; returns the structured denial on failure.
func gate(role authz.Role, action authz.Action) error {
	if d := authz.Can(role, action); d != nil {
		return d
	}
	return nil
}

func (s *Service) trailers(a *Actor) map[string]string {
	return map[string]string{
		"Ubiqo-User":     a.Username,
		"Ubiqo-Identity": a.Identity,
		"Ubiqo-Server":   s.Version,
	}
}

// ─── WhoAmI ───

type WhoAmI struct {
	Username string            `json:"username"`
	Org      string            `json:"org"`
	OrgRole  string            `json:"org_role"`
	Identity string            `json:"identity"`
	Projects map[string]string `json:"projects"` // slug → role
}

func (s *Service) WhoAmI(ctx context.Context, a *Actor) (*WhoAmI, error) {
	projects, err := s.DB.ListProjects(ctx, a.OrgID)
	if err != nil {
		return nil, err
	}
	out := &WhoAmI{
		Username: a.Username, Org: a.OrgSlug, OrgRole: string(a.OrgRole),
		Identity: a.Identity, Projects: map[string]string{},
	}
	for _, p := range projects {
		role, err := s.DB.ProjectRole(ctx, p.ID, a.UserID)
		if err != nil {
			return nil, err
		}
		if role == authz.RoleNone && authz.OrgCanManage(a.OrgRole) {
			role = authz.RoleMaintainer
		}
		if role != authz.RoleNone {
			out.Projects[p.Slug] = string(role)
		}
	}
	return out, nil
}

// ─── Context bundle ───

type ContextBundle struct {
	BundleVersion string `json:"bundle_version"`
	Instructions  string `json:"instructions"`
	Digest        string `json:"digest,omitempty"`
	Memories      string `json:"memories,omitempty"`
}

// GetContext compiles instructions (cache-free in P0 — compilation is cheap)
// and assembles the framed digest + memory recall.
func (s *Service) GetContext(ctx context.Context, a *Actor, projectSlug, section string) (*ContextBundle, error) {
	p, role, err := s.projectCtx(ctx, a, projectSlug)
	if err != nil {
		return nil, err
	}
	if err := gate(role, authz.ContextRead); err != nil {
		return nil, err
	}
	org, err := s.DB.GetOrgBySlug(ctx, a.OrgSlug)
	if err != nil {
		return nil, err
	}
	members, err := s.DB.ProjectMembers(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	artNames, err := s.Git.ListArtifacts(ctx, a.OrgSlug, p.Slug)
	if err != nil {
		return nil, err
	}
	latest, err := s.DB.LatestReleases(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	open, err := s.DB.ListProposals(ctx, p.ID, "open")
	if err != nil {
		return nil, err
	}
	openCount := map[string]int{}
	for _, pr := range open {
		openCount[pr.Artifact]++
	}
	in := compiler.Input{
		OrgSlug: org.Slug, OrgName: org.Name, OrgInstructions: org.Instructions,
		ProjectSlug: p.Slug, ProjectName: p.Name, ProjectInstructions: p.Instructions,
		SoloMode: p.SoloMode, ServerVersion: s.Version,
	}
	for _, m := range members {
		in.Members = append(in.Members, compiler.Member{Username: m.Username, Role: m.Role})
	}
	for _, name := range artNames {
		in.Artifacts = append(in.Artifacts, compiler.Artifact{
			Name: name, LatestVersion: latest[name].Version, OpenProposals: openCount[name],
		})
	}
	b := compiler.Compile(in)

	out := &ContextBundle{BundleVersion: b.Version}
	header := compiler.IdentityHeader(a.Username, string(role), p.SoloMode)
	out.Instructions = header + b.ClaudeMD

	if section == "" || section == "full" || section == "digest" {
		digest, err := s.activityDigest(ctx, p, 15)
		if err != nil {
			return nil, err
		}
		out.Digest = digest
		mems, err := s.DB.Recall(ctx, a.OrgID, a.UserID, &p.ID, "", 5)
		if err != nil {
			return nil, err
		}
		out.Memories = frameMemories(mems)
	}
	if section == "instructions" {
		out.Digest, out.Memories = "", ""
	}
	if section == "digest" {
		out.Instructions = header + "(instructions omitted; bundle " + b.Version + ")"
	}
	return out, nil
}

func (s *Service) activityDigest(ctx context.Context, p *store.Project, limit int) (string, error) {
	events, err := s.DB.RecentEvents(ctx, p.ID, limit)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "", nil
	}
	var b strings.Builder
	for i := len(events) - 1; i >= 0; i-- { // chronological
		e := events[i]
		id := e.ActorUsername
		if e.IdentityLabel != "" {
			id += " via " + e.IdentityLabel
		}
		fmt.Fprintf(&b, "[%s] %s (%s): %s\n",
			e.CreatedAt.UTC().Format("2006-01-02 15:04"), e.Kind, id, strings.ReplaceAll(e.Summary, "\n", " "))
	}
	return "### Recent team activity\n" + compiler.Frame("ubiqo activity feed", strings.TrimSpace(b.String())), nil
}

func frameMemories(mems []store.Memory) string {
	if len(mems) == 0 {
		return ""
	}
	var b strings.Builder
	for i := len(mems) - 1; i >= 0; i-- {
		m := mems[i]
		fmt.Fprintf(&b, "- (%s, by %s, %s) %s\n", m.Scope, m.CreatedBy,
			m.CreatedAt.UTC().Format("2006-01-02"), strings.ReplaceAll(m.Content, "\n", " "))
	}
	return "### Memories\n" + compiler.Frame("ubiqo memories", strings.TrimSpace(b.String()))
}

// ─── Artifacts ───

type ArtifactList struct {
	Artifacts []ArtifactInfo `json:"artifacts"`
}
type ArtifactInfo struct {
	Name          string `json:"name"`
	LatestRelease string `json:"latest_release,omitempty"`
	ReadPinned    string `json:"read_pinned_with_ref,omitempty"`
}

func (s *Service) ListArtifacts(ctx context.Context, a *Actor, projectSlug string) (*ArtifactList, error) {
	p, role, err := s.projectCtx(ctx, a, projectSlug)
	if err != nil {
		return nil, err
	}
	if err := gate(role, authz.ArtifactRead); err != nil {
		return nil, err
	}
	names, err := s.Git.ListArtifacts(ctx, a.OrgSlug, p.Slug)
	if err != nil {
		return nil, err
	}
	latest, err := s.DB.LatestReleases(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	out := &ArtifactList{Artifacts: []ArtifactInfo{}}
	for _, n := range names {
		info := ArtifactInfo{Name: n, LatestRelease: latest[n].Version}
		if latest[n].Version != "" {
			info.ReadPinned = latest[n].Tag
		}
		out.Artifacts = append(out.Artifacts, info)
	}
	return out, nil
}

type ArtifactContent struct {
	Artifact string              `json:"artifact"`
	Ref      string              `json:"ref"`
	Files    []gitstore.TreeEntry `json:"files,omitempty"`
	Path     string              `json:"path,omitempty"`
	Content  string              `json:"content,omitempty"`
}

// ReadArtifact lists the tree (no path) or returns one framed file (path).
func (s *Service) ReadArtifact(ctx context.Context, a *Actor, projectSlug, artifact, ref, path string) (*ArtifactContent, error) {
	p, role, err := s.projectCtx(ctx, a, projectSlug)
	if err != nil {
		return nil, err
	}
	if err := gate(role, authz.ArtifactRead); err != nil {
		return nil, err
	}
	if ref == "" {
		ref = "main"
	}
	out := &ArtifactContent{Artifact: artifact, Ref: ref}
	if path == "" {
		tree, err := s.Git.ListTree(ctx, a.OrgSlug, p.Slug, ref, artifact)
		if err != nil {
			return nil, err
		}
		out.Files = tree
		return out, nil
	}
	content, err := s.Git.ReadFile(ctx, a.OrgSlug, p.Slug, ref, artifact, path)
	if err != nil {
		return nil, err
	}
	out.Path = path
	out.Content = compiler.Frame("artifact "+artifact+"/"+path+"@"+ref, content)
	return out, nil
}

type WriteResult struct {
	CommitSHA   string `json:"commit_sha"`
	Branch      string `json:"branch"`
	WroteToMain bool   `json:"wrote_to_main"`
	NextStep    string `json:"next_step,omitempty"`
}

func (s *Service) WriteArtifact(ctx context.Context, a *Actor, projectSlug, artifact, message string, files []gitstore.File) (*WriteResult, error) {
	p, role, err := s.projectCtx(ctx, a, projectSlug)
	if err != nil {
		return nil, err
	}
	if err := gate(role, authz.ArtifactWrite); err != nil {
		return nil, err
	}
	branch := "main"
	if !p.SoloMode {
		branch = gitstore.ProposalBranch(a.Username, artifact)
	}
	sha, err := s.Git.WriteFiles(ctx, a.OrgSlug, p.Slug, branch, artifact, files, message, s.trailers(a))
	if err != nil {
		return nil, err
	}
	res := &WriteResult{CommitSHA: sha, Branch: branch, WroteToMain: branch == "main"}
	summary := fmt.Sprintf("wrote %d file(s) to %s on %s: %s", len(files), artifact, branch, firstLine(message))
	_ = s.DB.AppendEvent(ctx, a.OrgID, &p.ID, &a.UserID, a.Identity, "artifact.written", summary)
	if !res.WroteToMain {
		res.NextStep = "changes are on your branch; call ubiqo_propose_merge to submit them for review"
	}
	return res, nil
}

// ─── Proposals ───

type ProposalResult struct {
	ProposalID string `json:"proposal_id"`
	Branch     string `json:"branch"`
	Status     string `json:"status"`
	NextStep   string `json:"next_step,omitempty"`
}

func (s *Service) ProposeMerge(ctx context.Context, a *Actor, projectSlug, artifact, summary string) (*ProposalResult, error) {
	p, role, err := s.projectCtx(ctx, a, projectSlug)
	if err != nil {
		return nil, err
	}
	if err := gate(role, authz.ProposalCreate); err != nil {
		return nil, err
	}
	if p.SoloMode {
		return nil, fmt.Errorf("this project is in solo mode; ubiqo_write_artifact already writes to main — no proposal needed")
	}
	branch := gitstore.ProposalBranch(a.Username, artifact)
	id, err := s.DB.CreateProposal(ctx, a.OrgID, p.ID, artifact, branch, a.UserID, summary)
	if err != nil {
		return nil, err
	}
	_ = s.DB.AppendEvent(ctx, a.OrgID, &p.ID, &a.UserID, a.Identity, "proposal.created",
		fmt.Sprintf("proposed merge of %s (%s): %s", artifact, firstLine(summary), id))
	return &ProposalResult{
		ProposalID: id.String(), Branch: branch, Status: "open",
		NextStep: "a maintainer reviews with ubiqo_list_proposals / ubiqo_merge_proposal",
	}, nil
}

type ProposalView struct {
	ID        string   `json:"id"`
	Artifact  string   `json:"artifact"`
	Proposer  string   `json:"proposer"`
	Summary   string   `json:"summary"`
	Status    string   `json:"status"`
	Conflicts []string `json:"conflicts,omitempty"`
	Diff      string   `json:"diff,omitempty"`
	CreatedAt string   `json:"created_at"`
}

func (s *Service) ListProposals(ctx context.Context, a *Actor, projectSlug, status string, withDiff bool) ([]ProposalView, error) {
	p, role, err := s.projectCtx(ctx, a, projectSlug)
	if err != nil {
		return nil, err
	}
	if err := gate(role, authz.ArtifactRead); err != nil {
		return nil, err
	}
	props, err := s.DB.ListProposals(ctx, p.ID, status)
	if err != nil {
		return nil, err
	}
	out := []ProposalView{}
	for _, pr := range props {
		v := ProposalView{
			ID: pr.ID.String(), Artifact: pr.Artifact, Proposer: pr.Proposer,
			Summary: pr.Summary, Status: pr.Status, Conflicts: pr.ConflictFiles,
			CreatedAt: pr.CreatedAt.UTC().Format(time.RFC3339),
		}
		if withDiff && pr.Status == "open" {
			diff, err := s.Git.DiffStat(ctx, a.OrgSlug, p.Slug, pr.Branch)
			if err == nil {
				v.Diff = compiler.Frame("proposal diff "+pr.ID.String(), diff)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

type MergeOutcome struct {
	Status    string   `json:"status"` // merged | conflicted
	CommitSHA string   `json:"commit_sha,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
	NextStep  string   `json:"next_step,omitempty"`
}

func (s *Service) MergeProposal(ctx context.Context, a *Actor, projectSlug, proposalID string) (*MergeOutcome, error) {
	p, role, err := s.projectCtx(ctx, a, projectSlug)
	if err != nil {
		return nil, err
	}
	if err := gate(role, authz.ProposalMerge); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(proposalID)
	if err != nil {
		return nil, fmt.Errorf("invalid proposal id %q", proposalID)
	}
	pr, err := s.DB.GetProposal(ctx, p.ID, id)
	if err != nil {
		return nil, err
	}
	if pr.Status != "open" && pr.Status != "conflicted" {
		return nil, fmt.Errorf("proposal is %s; only open or conflicted proposals can be merged", pr.Status)
	}
	msg := fmt.Sprintf("merge %s: %s (proposal %s by %s)", pr.Artifact, firstLine(pr.Summary), pr.ID, pr.Proposer)
	res, err := s.Git.Merge(ctx, a.OrgSlug, p.Slug, pr.Branch, msg, s.trailers(a))
	if err != nil {
		return nil, err
	}
	if len(res.Conflicts) > 0 {
		_ = s.DB.ResolveProposal(ctx, pr.ID, "conflicted", a.UserID, res.Conflicts)
		_ = s.DB.AppendEvent(ctx, a.OrgID, &p.ID, &a.UserID, a.Identity, "proposal.conflicted",
			fmt.Sprintf("merge of %s hit conflicts in %s", pr.Artifact, strings.Join(res.Conflicts, ", ")))
		return &MergeOutcome{
			Status: "conflicted", Conflicts: res.Conflicts,
			NextStep: fmt.Sprintf("ask %s to update their branch: read the conflicting files at ref %q and at main, reconcile whole files with ubiqo_write_artifact, then merge again", pr.Proposer, pr.Branch),
		}, nil
	}
	_ = s.DB.ResolveProposal(ctx, pr.ID, "merged", a.UserID, nil)
	_ = s.Git.DeleteBranch(ctx, a.OrgSlug, p.Slug, pr.Branch)
	_ = s.DB.AppendEvent(ctx, a.OrgID, &p.ID, &a.UserID, a.Identity, "proposal.merged",
		fmt.Sprintf("merged %s (proposal by %s): %s", pr.Artifact, pr.Proposer, firstLine(pr.Summary)))
	return &MergeOutcome{Status: "merged", CommitSHA: res.SHA,
		NextStep: "consider ubiqo_release_artifact to cut a semver release consumers can pin"}, nil
}

// ─── Releases ───

type ReleaseResult struct {
	Artifact string `json:"artifact"`
	Version  string `json:"version"`
	Tag      string `json:"tag"`
}

func (s *Service) ReleaseArtifact(ctx context.Context, a *Actor, projectSlug, artifact, bump, notes string) (*ReleaseResult, error) {
	p, role, err := s.projectCtx(ctx, a, projectSlug)
	if err != nil {
		return nil, err
	}
	if err := gate(role, authz.ReleaseCreate); err != nil {
		return nil, err
	}
	versions, err := s.Git.Versions(ctx, a.OrgSlug, p.Slug, artifact)
	if err != nil {
		return nil, err
	}
	cur, ok := semver.Latest(versions)
	if !ok {
		cur = semver.Version{} // v0.0.0 → first bump yields v0.0.1 / v0.1.0 / v1.0.0
	}
	next, err := semver.Bump(cur, bump)
	if err != nil {
		return nil, err
	}
	tag, err := s.Git.Tag(ctx, a.OrgSlug, p.Slug, artifact, next.String(), notes)
	if err != nil {
		return nil, err
	}
	if err := s.DB.CreateRelease(ctx, a.OrgID, p.ID, artifact, next.String(), tag, notes, a.UserID); err != nil {
		return nil, err
	}
	_ = s.DB.AppendEvent(ctx, a.OrgID, &p.ID, &a.UserID, a.Identity, "release.published",
		fmt.Sprintf("released %s %s: %s", artifact, next.String(), firstLine(notes)))
	return &ReleaseResult{Artifact: artifact, Version: next.String(), Tag: tag}, nil
}

// ─── Memory ───

type MemoryResult struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
}

func (s *Service) Remember(ctx context.Context, a *Actor, projectSlug, scope, content string) (*MemoryResult, error) {
	p, role, err := s.projectCtx(ctx, a, projectSlug)
	if err != nil {
		return nil, err
	}
	if err := gate(role, authz.MemoryWrite); err != nil {
		return nil, err
	}
	var userID, projectID *uuid.UUID
	switch scope {
	case "user":
		userID = &a.UserID
	case "project":
		projectID = &p.ID
	default:
		return nil, fmt.Errorf("scope must be \"user\" (private) or \"project\" (shared with the team)")
	}
	id, err := s.DB.Remember(ctx, a.OrgID, scope, userID, projectID, strings.TrimSpace(content), a.UserID)
	if err != nil {
		return nil, err
	}
	return &MemoryResult{ID: id.String(), Scope: scope}, nil
}

type RecallResult struct {
	Memories string `json:"memories"`
	Count    int    `json:"count"`
}

func (s *Service) Recall(ctx context.Context, a *Actor, projectSlug, query string, limit int) (*RecallResult, error) {
	p, role, err := s.projectCtx(ctx, a, projectSlug)
	if err != nil {
		return nil, err
	}
	if err := gate(role, authz.MemoryRead); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	mems, err := s.DB.Recall(ctx, a.OrgID, a.UserID, &p.ID, query, limit)
	if err != nil {
		return nil, err
	}
	return &RecallResult{Memories: frameMemories(mems), Count: len(mems)}, nil
}

// ─── Activity ───

type ActivityResult struct {
	Digest string `json:"digest"`
	Count  int    `json:"count"`
}

func (s *Service) Activity(ctx context.Context, a *Actor, projectSlug string, limit int) (*ActivityResult, error) {
	p, role, err := s.projectCtx(ctx, a, projectSlug)
	if err != nil {
		return nil, err
	}
	if err := gate(role, authz.ActivityRead); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	events, err := s.DB.RecentEvents(ctx, p.ID, limit)
	if err != nil {
		return nil, err
	}
	digest, err := s.activityDigest(ctx, p, limit)
	if err != nil {
		return nil, err
	}
	return &ActivityResult{Digest: digest, Count: len(events)}, nil
}

// RecordSessionEnd ingests a session summary (REST hook path).
func (s *Service) RecordSessionEnd(ctx context.Context, a *Actor, projectSlug, summary string) error {
	p, role, err := s.projectCtx(ctx, a, projectSlug)
	if err != nil {
		return err
	}
	if err := gate(role, authz.ActivityRead); err != nil { // any member may report
		return err
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}
	if len(summary) > 2000 {
		summary = summary[:2000]
	}
	return s.DB.AppendEvent(ctx, a.OrgID, &p.ID, &a.UserID, a.Identity, "session.summary", summary)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
