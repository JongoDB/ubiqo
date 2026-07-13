-- ubiqo control plane, migration 0001.
-- Conventions (ADRs 0004/0006, security review): org_id on every tenant
-- table; merge_proposals/releases are PROJECTIONS of git state (rebuildable);
-- events are append-only (trigger-enforced).

create extension if not exists pgcrypto;

create table orgs (
    id          uuid primary key default gen_random_uuid(),
    slug        text not null unique check (slug ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
    name        text not null,
    instructions text not null default '',
    created_at  timestamptz not null default now()
);

create table users (
    id           uuid primary key default gen_random_uuid(),
    username     text not null unique check (username ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
    display_name text not null default '',
    created_at   timestamptz not null default now()
);

create table org_memberships (
    org_id     uuid not null references orgs(id) on delete cascade,
    user_id    uuid not null references users(id) on delete cascade,
    role       text not null check (role in ('owner','admin','member','guest')),
    created_at timestamptz not null default now(),
    primary key (org_id, user_id)
);

create table projects (
    id           uuid primary key default gen_random_uuid(),
    org_id       uuid not null references orgs(id) on delete cascade,
    slug         text not null check (slug ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
    name         text not null,
    instructions text not null default '',
    solo_mode    boolean not null default false,
    created_at   timestamptz not null default now(),
    unique (org_id, slug)
);

create table project_memberships (
    project_id uuid not null references projects(id) on delete cascade,
    org_id     uuid not null references orgs(id) on delete cascade,
    user_id    uuid not null references users(id) on delete cascade,
    role       text not null check (role in ('maintainer','contributor','viewer')),
    created_at timestamptz not null default now(),
    primary key (project_id, user_id)
);

create table device_tokens (
    id          uuid primary key default gen_random_uuid(),
    org_id      uuid not null references orgs(id) on delete cascade,
    user_id     uuid not null references users(id) on delete cascade,
    token_hash  bytea not null unique,
    label       text not null default '',
    client_kind text not null default '',
    created_at  timestamptz not null default now(),
    last_seen_at timestamptz,
    revoked_at  timestamptz
);
create index device_tokens_user on device_tokens (user_id);

create table events (
    id             bigint generated always as identity primary key,
    org_id         uuid not null references orgs(id) on delete cascade,
    project_id     uuid references projects(id) on delete cascade,
    actor_user_id  uuid references users(id),
    identity_label text not null default '',
    kind           text not null,
    summary        text not null,
    payload        jsonb not null default '{}',
    created_at     timestamptz not null default now()
);
create index events_project_time on events (project_id, id desc);

-- Append-only enforcement (security review): convention becomes control.
create function ubiqo_immutable() returns trigger language plpgsql as $$
begin
    raise exception 'events are append-only';
end $$;
create trigger events_immutable
    before update or delete on events
    for each row execute function ubiqo_immutable();

create table memories (
    id         uuid primary key default gen_random_uuid(),
    org_id     uuid not null references orgs(id) on delete cascade,
    scope      text not null check (scope in ('user','project','org')),
    user_id    uuid references users(id) on delete cascade,
    project_id uuid references projects(id) on delete cascade,
    content    text not null check (length(content) between 1 and 4000),
    created_by uuid references users(id),
    created_at timestamptz not null default now(),
    deleted_at timestamptz,
    tsv        tsvector generated always as (to_tsvector('english', content)) stored,
    check ((scope = 'user' and user_id is not null)
        or (scope = 'project' and project_id is not null)
        or (scope = 'org'))
);
create index memories_tsv on memories using gin (tsv);
create index memories_scope on memories (org_id, scope, project_id, user_id);

-- Projections of git state (ADR-0004). Rebuildable via `ubiqo fsck`.
create table merge_proposals (
    id          uuid primary key default gen_random_uuid(),
    org_id      uuid not null references orgs(id) on delete cascade,
    project_id  uuid not null references projects(id) on delete cascade,
    artifact    text not null,
    branch      text not null,
    proposer    uuid not null references users(id),
    summary     text not null default '',
    status      text not null default 'open' check (status in ('open','merged','rejected','conflicted')),
    conflict_files text[] not null default '{}',
    created_at  timestamptz not null default now(),
    resolved_at timestamptz,
    resolved_by uuid references users(id)
);
create index merge_proposals_project on merge_proposals (project_id, status);

create table releases (
    id         uuid primary key default gen_random_uuid(),
    org_id     uuid not null references orgs(id) on delete cascade,
    project_id uuid not null references projects(id) on delete cascade,
    artifact   text not null,
    version    text not null,
    tag        text not null,
    notes      text not null default '',
    created_by uuid references users(id),
    created_at timestamptz not null default now(),
    unique (project_id, artifact, version)
);
