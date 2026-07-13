// Package store is the Postgres control plane: identity, hierarchy, tokens,
// events, memories, and the git-state projections (ADR-0004).
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jongodb/ubiqo/internal/authz"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var ErrNotFound = errors.New("not found")

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()                          { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error  { return s.pool.Ping(ctx) }

// Migrate applies embedded migrations in filename order, forward-only,
// tracked in schema_migrations.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx,
		`create table if not exists schema_migrations (name text primary key, applied_at timestamptz not null default now())`); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var exists bool
		if err := s.pool.QueryRow(ctx, `select exists(select 1 from schema_migrations where name=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sql, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `insert into schema_migrations(name) values($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ─── Entities ───

type Org struct {
	ID           uuid.UUID
	Slug, Name   string
	Instructions string
}

type User struct {
	ID                    uuid.UUID
	Username, DisplayName string
}

type Project struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	Slug, Name   string
	Instructions string
	SoloMode     bool
}

type Member struct {
	Username string
	Role     string
}

type Memory struct {
	ID        uuid.UUID
	Scope     string
	Content   string
	CreatedBy string
	CreatedAt time.Time
}

type Event struct {
	ID            int64
	Kind          string
	Summary       string
	ActorUsername string
	IdentityLabel string
	CreatedAt     time.Time
}

type Proposal struct {
	ID            uuid.UUID
	Artifact      string
	Branch        string
	Proposer      string
	Summary       string
	Status        string
	ConflictFiles []string
	CreatedAt     time.Time
}

type Release struct {
	Artifact  string
	Version   string
	Tag       string
	Notes     string
	CreatedBy string
	CreatedAt time.Time
}

// ─── Orgs / users / projects / memberships ───

func (s *Store) CreateOrg(ctx context.Context, slug, name string) (*Org, error) {
	o := &Org{Slug: slug, Name: name}
	err := s.pool.QueryRow(ctx,
		`insert into orgs (slug, name) values ($1,$2) returning id`, slug, name).Scan(&o.ID)
	return o, err
}

func (s *Store) GetOrgBySlug(ctx context.Context, slug string) (*Org, error) {
	o := &Org{}
	err := s.pool.QueryRow(ctx,
		`select id, slug, name, instructions from orgs where slug=$1`, slug).
		Scan(&o.ID, &o.Slug, &o.Name, &o.Instructions)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

func (s *Store) FirstOrg(ctx context.Context) (*Org, error) {
	o := &Org{}
	err := s.pool.QueryRow(ctx,
		`select id, slug, name, instructions from orgs order by created_at limit 1`).
		Scan(&o.ID, &o.Slug, &o.Name, &o.Instructions)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

func (s *Store) CreateUser(ctx context.Context, username, display string) (*User, error) {
	u := &User{Username: username, DisplayName: display}
	err := s.pool.QueryRow(ctx,
		`insert into users (username, display_name) values ($1,$2) returning id`, username, display).Scan(&u.ID)
	return u, err
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	u := &User{}
	err := s.pool.QueryRow(ctx,
		`select id, username, display_name from users where username=$1`, username).
		Scan(&u.ID, &u.Username, &u.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) AddOrgMember(ctx context.Context, orgID, userID uuid.UUID, role string) error {
	if !authz.ValidOrgRole(role) {
		return fmt.Errorf("invalid org role %q", role)
	}
	_, err := s.pool.Exec(ctx,
		`insert into org_memberships (org_id, user_id, role) values ($1,$2,$3)
		 on conflict (org_id, user_id) do update set role=excluded.role`, orgID, userID, role)
	return err
}

func (s *Store) OrgRole(ctx context.Context, orgID, userID uuid.UUID) (authz.OrgRole, error) {
	var role string
	err := s.pool.QueryRow(ctx,
		`select role from org_memberships where org_id=$1 and user_id=$2`, orgID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return authz.OrgNone, nil
	}
	return authz.OrgRole(role), err
}

func (s *Store) OrgMemberCount(ctx context.Context, orgID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `select count(*) from org_memberships where org_id=$1`, orgID).Scan(&n)
	return n, err
}

func (s *Store) CreateProject(ctx context.Context, orgID uuid.UUID, slug, name string, solo bool) (*Project, error) {
	p := &Project{OrgID: orgID, Slug: slug, Name: name, SoloMode: solo}
	err := s.pool.QueryRow(ctx,
		`insert into projects (org_id, slug, name, solo_mode) values ($1,$2,$3,$4) returning id`,
		orgID, slug, name, solo).Scan(&p.ID)
	return p, err
}

func (s *Store) GetProject(ctx context.Context, orgID uuid.UUID, slug string) (*Project, error) {
	p := &Project{}
	err := s.pool.QueryRow(ctx,
		`select id, org_id, slug, name, instructions, solo_mode from projects where org_id=$1 and slug=$2`,
		orgID, slug).Scan(&p.ID, &p.OrgID, &p.Slug, &p.Name, &p.Instructions, &p.SoloMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func (s *Store) ListProjects(ctx context.Context, orgID uuid.UUID) ([]Project, error) {
	rows, err := s.pool.Query(ctx,
		`select id, org_id, slug, name, instructions, solo_mode from projects where org_id=$1 order by slug`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Slug, &p.Name, &p.Instructions, &p.SoloMode); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) AddProjectMember(ctx context.Context, p *Project, userID uuid.UUID, role string) error {
	if !authz.ValidRole(role) {
		return fmt.Errorf("invalid project role %q", role)
	}
	_, err := s.pool.Exec(ctx,
		`insert into project_memberships (project_id, org_id, user_id, role) values ($1,$2,$3,$4)
		 on conflict (project_id, user_id) do update set role=excluded.role`, p.ID, p.OrgID, userID, role)
	return err
}

func (s *Store) ProjectRole(ctx context.Context, projectID, userID uuid.UUID) (authz.Role, error) {
	var role string
	err := s.pool.QueryRow(ctx,
		`select role from project_memberships where project_id=$1 and user_id=$2`, projectID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return authz.RoleNone, nil
	}
	return authz.Role(role), err
}

func (s *Store) ProjectMembers(ctx context.Context, projectID uuid.UUID) ([]Member, error) {
	rows, err := s.pool.Query(ctx,
		`select u.username, m.role from project_memberships m join users u on u.id=m.user_id
		 where m.project_id=$1 order by u.username`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.Username, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ─── Device tokens (ADR-0003) ───

// NewToken mints a device token, storing only its SHA-256. The plaintext
// (returned once) has the form ubq_<64 hex chars>.
func (s *Store) NewToken(ctx context.Context, orgID, userID uuid.UUID, label, clientKind string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	plain := "ubq_" + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plain))
	_, err := s.pool.Exec(ctx,
		`insert into device_tokens (org_id, user_id, token_hash, label, client_kind) values ($1,$2,$3,$4,$5)`,
		orgID, userID, sum[:], label, clientKind)
	if err != nil {
		return "", err
	}
	return plain, nil
}

type TokenIdentity struct {
	UserID   uuid.UUID
	Username string
	OrgID    uuid.UUID
	OrgSlug  string
	OrgRole  authz.OrgRole
	Label    string
}

// Authenticate resolves a bearer token to an identity, updating last_seen.
func (s *Store) Authenticate(ctx context.Context, token string) (*TokenIdentity, error) {
	sum := sha256.Sum256([]byte(token))
	ti := &TokenIdentity{}
	var tokenID uuid.UUID
	var hash []byte
	err := s.pool.QueryRow(ctx,
		`select t.id, t.token_hash, t.label, u.id, u.username, o.id, o.slug, coalesce(m.role,'')
		 from device_tokens t
		 join users u on u.id = t.user_id
		 join orgs o on o.id = t.org_id
		 left join org_memberships m on m.org_id = t.org_id and m.user_id = t.user_id
		 where t.token_hash = $1 and t.revoked_at is null`, sum[:]).
		Scan(&tokenID, &hash, &ti.Label, &ti.UserID, &ti.Username, &ti.OrgID, &ti.OrgSlug, (*string)(&ti.OrgRole))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(hash, sum[:]) != 1 { // defense in depth
		return nil, ErrNotFound
	}
	_, _ = s.pool.Exec(ctx, `update device_tokens set last_seen_at=now() where id=$1`, tokenID)
	return ti, nil
}

func (s *Store) RevokeToken(ctx context.Context, id uuid.UUID) error {
	ct, err := s.pool.Exec(ctx, `update device_tokens set revoked_at=now() where id=$1 and revoked_at is null`, id)
	if err == nil && ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ListTokens(ctx context.Context, orgID uuid.UUID) ([]map[string]string, error) {
	rows, err := s.pool.Query(ctx,
		`select t.id, u.username, t.label, t.client_kind,
		        coalesce(to_char(t.last_seen_at,'YYYY-MM-DD HH24:MI'),'never'),
		        case when t.revoked_at is null then 'active' else 'revoked' end
		 from device_tokens t join users u on u.id=t.user_id where t.org_id=$1 order by t.created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]string
	for rows.Next() {
		var id uuid.UUID
		var username, label, kind, seen, state string
		if err := rows.Scan(&id, &username, &label, &kind, &seen, &state); err != nil {
			return nil, err
		}
		out = append(out, map[string]string{
			"id": id.String(), "user": username, "label": label,
			"client": kind, "last_seen": seen, "state": state,
		})
	}
	return out, rows.Err()
}

// ─── Events ───

func (s *Store) AppendEvent(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, actor *uuid.UUID, identityLabel, kind, summary string) error {
	_, err := s.pool.Exec(ctx,
		`insert into events (org_id, project_id, actor_user_id, identity_label, kind, summary)
		 values ($1,$2,$3,$4,$5,$6)`, orgID, projectID, actor, identityLabel, kind, summary)
	return err
}

func (s *Store) RecentEvents(ctx context.Context, projectID uuid.UUID, limit int) ([]Event, error) {
	rows, err := s.pool.Query(ctx,
		`select e.id, e.kind, e.summary, coalesce(u.username,'system'), e.identity_label, e.created_at
		 from events e left join users u on u.id=e.actor_user_id
		 where e.project_id=$1 order by e.id desc limit $2`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Kind, &e.Summary, &e.ActorUsername, &e.IdentityLabel, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ─── Memories (ADR-0007: native FTS first) ───

func (s *Store) Remember(ctx context.Context, orgID uuid.UUID, scope string, userID, projectID *uuid.UUID, content string, createdBy uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`insert into memories (org_id, scope, user_id, project_id, content, created_by)
		 values ($1,$2,$3,$4,$5,$6) returning id`,
		orgID, scope, userID, projectID, content, createdBy).Scan(&id)
	return id, err
}

// Recall returns memories visible to userID in the given project: their own
// user-scoped ones plus project- and org-scoped ones. Empty query = recent.
func (s *Store) Recall(ctx context.Context, orgID, userID uuid.UUID, projectID *uuid.UUID, query string, limit int) ([]Memory, error) {
	base := `
		select m.id, m.scope, m.content, coalesce(u.username,''), m.created_at
		from memories m left join users u on u.id = m.created_by
		where m.org_id = $1 and m.deleted_at is null
		  and (   (m.scope = 'user' and m.user_id = $2)
		       or (m.scope = 'project' and m.project_id = $3)
		       or (m.scope = 'org'))`
	var rows pgx.Rows
	var err error
	if strings.TrimSpace(query) == "" {
		rows, err = s.pool.Query(ctx, base+` order by m.created_at desc limit $4`, orgID, userID, projectID, limit)
	} else {
		rows, err = s.pool.Query(ctx, base+`
		  and m.tsv @@ websearch_to_tsquery('english', $4)
		order by ts_rank(m.tsv, websearch_to_tsquery('english', $4)) desc, m.created_at desc
		limit $5`, orgID, userID, projectID, query, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Scope, &m.Content, &m.CreatedBy, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ─── Projections: proposals & releases (ADR-0004) ───

func (s *Store) CreateProposal(ctx context.Context, orgID, projectID uuid.UUID, artifact, branch string, proposer uuid.UUID, summary string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`insert into merge_proposals (org_id, project_id, artifact, branch, proposer, summary)
		 values ($1,$2,$3,$4,$5,$6) returning id`,
		orgID, projectID, artifact, branch, proposer, summary).Scan(&id)
	return id, err
}

func (s *Store) GetProposal(ctx context.Context, projectID, id uuid.UUID) (*Proposal, error) {
	p := &Proposal{}
	err := s.pool.QueryRow(ctx,
		`select mp.id, mp.artifact, mp.branch, u.username, mp.summary, mp.status, mp.conflict_files, mp.created_at
		 from merge_proposals mp join users u on u.id=mp.proposer
		 where mp.project_id=$1 and mp.id=$2`, projectID, id).
		Scan(&p.ID, &p.Artifact, &p.Branch, &p.Proposer, &p.Summary, &p.Status, &p.ConflictFiles, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func (s *Store) ListProposals(ctx context.Context, projectID uuid.UUID, status string) ([]Proposal, error) {
	rows, err := s.pool.Query(ctx,
		`select mp.id, mp.artifact, mp.branch, u.username, mp.summary, mp.status, mp.conflict_files, mp.created_at
		 from merge_proposals mp join users u on u.id=mp.proposer
		 where mp.project_id=$1 and ($2='' or mp.status=$2) order by mp.created_at`, projectID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		var p Proposal
		if err := rows.Scan(&p.ID, &p.Artifact, &p.Branch, &p.Proposer, &p.Summary, &p.Status, &p.ConflictFiles, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ResolveProposal(ctx context.Context, id uuid.UUID, status string, resolvedBy uuid.UUID, conflictFiles []string) error {
	if conflictFiles == nil {
		conflictFiles = []string{}
	}
	_, err := s.pool.Exec(ctx,
		`update merge_proposals set status=$2, resolved_at=now(), resolved_by=$3, conflict_files=$4 where id=$1`,
		id, status, resolvedBy, conflictFiles)
	return err
}

func (s *Store) CreateRelease(ctx context.Context, orgID, projectID uuid.UUID, artifact, version, tag, notes string, createdBy uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`insert into releases (org_id, project_id, artifact, version, tag, notes, created_by)
		 values ($1,$2,$3,$4,$5,$6,$7)`, orgID, projectID, artifact, version, tag, notes, createdBy)
	return err
}

func (s *Store) LatestReleases(ctx context.Context, projectID uuid.UUID) (map[string]Release, error) {
	rows, err := s.pool.Query(ctx,
		`select distinct on (artifact) artifact, version, tag, notes, coalesce(u.username,''), r.created_at
		 from releases r left join users u on u.id=r.created_by
		 where project_id=$1 order by artifact, r.created_at desc`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Release{}
	for rows.Next() {
		var r Release
		if err := rows.Scan(&r.Artifact, &r.Version, &r.Tag, &r.Notes, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		out[r.Artifact] = r
	}
	return out, rows.Err()
}

func (s *Store) SetOrgInstructions(ctx context.Context, orgID uuid.UUID, text string) error {
	_, err := s.pool.Exec(ctx, `update orgs set instructions=$2 where id=$1`, orgID, text)
	return err
}

func (s *Store) SetProjectInstructions(ctx context.Context, projectID uuid.UUID, text string) error {
	_, err := s.pool.Exec(ctx, `update projects set instructions=$2 where id=$1`, projectID, text)
	return err
}

func (s *Store) ListOrgs(ctx context.Context) ([]Org, error) {
	rows, err := s.pool.Query(ctx, `select id, slug, name, instructions from orgs order by slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Org
	for rows.Next() {
		var o Org
		if err := rows.Scan(&o.ID, &o.Slug, &o.Name, &o.Instructions); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// EnsureRelease inserts a projection row if missing (fsck). Returns true if
// it repaired (inserted) the row.
func (s *Store) EnsureRelease(ctx context.Context, orgID, projectID uuid.UUID, artifact, version, tag string) (bool, error) {
	ct, err := s.pool.Exec(ctx,
		`insert into releases (org_id, project_id, artifact, version, tag, notes)
		 values ($1,$2,$3,$4,$5,'(rebuilt by fsck)')
		 on conflict (project_id, artifact, version) do nothing`,
		orgID, projectID, artifact, version, tag)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}

// ResetForTest drops and recreates the public schema. Guarded so it can
// never run against a non-test database.
func (s *Store) ResetForTest(ctx context.Context) error {
	var db string
	if err := s.pool.QueryRow(ctx, `select current_database()`).Scan(&db); err != nil {
		return err
	}
	if !strings.HasSuffix(db, "_test") {
		return fmt.Errorf("ResetForTest refuses to run on %q (database name must end in _test)", db)
	}
	_, err := s.pool.Exec(ctx, `drop schema public cascade; create schema public`)
	return err
}
