// Ops commands: seed (the "day at Acme" fixture — demo and test corpus),
// backup (pg_dump + git bundles + manifest), fsck (rebuild projections from
// git, ADR-0004).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jongodb/ubiqo/internal/core"
	"github.com/jongodb/ubiqo/internal/gitstore"
)

// cmdSeed loads the demo fixture through the real service flows (QA review:
// the walkthrough is the master fixture). Prints both device tokens.
func cmdSeed(args []string) error {
	return withService(func(ctx context.Context, svc *core.Service) error {
		org, err := svc.DB.CreateOrg(ctx, "acme", "Acme")
		if err != nil {
			return fmt.Errorf("seed expects an empty database: %w", err)
		}
		_ = svc.DB.SetOrgInstructions(ctx, org.ID, "Prefer boring technology. Write decisions down before acting on them.")
		jon, err := svc.DB.CreateUser(ctx, "jon", "Jon")
		if err != nil {
			return err
		}
		sam, err := svc.DB.CreateUser(ctx, "sam", "Sam")
		if err != nil {
			return err
		}
		if err := svc.DB.AddOrgMember(ctx, org.ID, jon.ID, "owner"); err != nil {
			return err
		}
		if err := svc.DB.AddOrgMember(ctx, org.ID, sam.ID, "member"); err != nil {
			return err
		}
		p, err := svc.DB.CreateProject(ctx, org.ID, "website-redesign", "Website Redesign", false)
		if err != nil {
			return err
		}
		_ = svc.DB.SetProjectInstructions(ctx, p.ID, "Client prefers understated design. All public copy is reviewed by Sam.")
		if err := svc.DB.AddProjectMember(ctx, p, jon.ID, "contributor"); err != nil {
			return err
		}
		if err := svc.DB.AddProjectMember(ctx, p, sam.ID, "maintainer"); err != nil {
			return err
		}
		if err := svc.Git.EnsureRepo(ctx, org.Slug, p.Slug); err != nil {
			return err
		}

		actorJon := &core.Actor{UserID: jon.ID, Username: "jon", OrgID: org.ID, OrgSlug: "acme", OrgRole: "owner", Identity: "claude-desktop/laptop-1"}
		actorSam := &core.Actor{UserID: sam.ID, Username: "sam", OrgID: org.ID, OrgSlug: "acme", OrgRole: "member", Identity: "claude-code/studio"}

		// Sam ships brand-kit v0.1.0 through the real propose→merge→release flow.
		if _, err := svc.WriteArtifact(ctx, actorSam, p.Slug, "brand-kit", "initial brand palette and voice",
			[]gitstore.File{{Path: "README.md", Content: "# Brand kit\n\nPalette: slate + amber. Voice: plain, confident.\n"}}); err != nil {
			return err
		}
		prop, err := svc.ProposeMerge(ctx, actorSam, p.Slug, "brand-kit", "initial brand kit")
		if err != nil {
			return err
		}
		if _, err := svc.MergeProposal(ctx, actorSam, p.Slug, prop.ProposalID); err != nil {
			return err
		}
		if _, err := svc.ReleaseArtifact(ctx, actorSam, p.Slug, "brand-kit", "minor", "first usable palette"); err != nil {
			return err
		}

		// Jon drafts api-design and leaves the proposal OPEN (review queue demo).
		if _, err := svc.WriteArtifact(ctx, actorJon, p.Slug, "api-design", "draft API surface",
			[]gitstore.File{{Path: "README.md", Content: "# API design\n\nDraft: REST, versioned under /v1.\n"}}); err != nil {
			return err
		}
		if _, err := svc.ProposeMerge(ctx, actorJon, p.Slug, "api-design", "first draft of the API surface — needs Sam's review"); err != nil {
			return err
		}

		if _, err := svc.Remember(ctx, actorJon, p.Slug, "project", "Client prefers Tailwind v4 for all frontend work."); err != nil {
			return err
		}
		_ = svc.RecordSessionEnd(ctx, actorJon, p.Slug, "drafted the API design and proposed it for review")

		tokJon, err := svc.DB.NewToken(ctx, org.ID, jon.ID, "claude-desktop/laptop-1", "seed")
		if err != nil {
			return err
		}
		tokSam, err := svc.DB.NewToken(ctx, org.ID, sam.ID, "claude-code/studio", "seed")
		if err != nil {
			return err
		}
		fmt.Printf(`seeded org "acme" / project "website-redesign" (team mode)

  jon (contributor, "account A"): %s
  sam (maintainer,  "account B"): %s

15-minute demo:
  claude mcp add --transport http ubiqo <server>/mcp --header "Authorization: Bearer <token>"
  → as sam, ask Claude: "what happened in website-redesign while I was away?"
  → it calls ubiqo_get_context and narrates jon's open proposal.
`, tokJon, tokSam)
		return nil
	})
}

func cmdBackup(args []string) error {
	out := "ubiqo-backup-" + time.Now().UTC().Format("20060102-150405")
	if len(args) >= 2 && args[0] == "--out" {
		out = args[1]
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		return err
	}
	dbURL := os.Getenv("UBIQO_DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("UBIQO_DATABASE_URL required")
	}
	dump := filepath.Join(out, "db.sql")
	cmd := exec.Command("pg_dump", "--no-owner", "--file", dump, dbURL)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pg_dump: %w: %s", err, string(b))
	}
	var bundles []string
	err := withService(func(ctx context.Context, svc *core.Service) error {
		var err error
		bundles, err = svc.Git.BundleAll(ctx, out)
		return err
	})
	if err != nil {
		return err
	}
	manifest := map[string]any{
		"ubiqo_version": Version,
		"created_at":    time.Now().UTC().Format(time.RFC3339),
		"database":      "db.sql",
		"git_bundles":   bundles,
		"restore":       "see docs/runbooks/restore.md",
	}
	b, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), b, 0o640); err != nil {
		return err
	}
	fmt.Printf("backup written to %s (%d git bundles + db.sql + manifest.json)\n", out, len(bundles))
	return nil
}

// cmdFsck rebuilds release projections from git tags and reports proposal
// rows whose branches no longer exist (git is the source of truth).
func cmdFsck(args []string) error {
	return withService(func(ctx context.Context, svc *core.Service) error {
		orgs, err := svc.DB.ListOrgs(ctx)
		if err != nil {
			return err
		}
		repaired, orphans := 0, 0
		for _, org := range orgs {
			projects, err := svc.DB.ListProjects(ctx, org.ID)
			if err != nil {
				return err
			}
			for _, p := range projects {
				arts, err := svc.Git.ListArtifacts(ctx, org.Slug, p.Slug)
				if err != nil {
					fmt.Printf("WARN %s/%s: %v\n", org.Slug, p.Slug, err)
					continue
				}
				for _, a := range arts {
					versions, err := svc.Git.Versions(ctx, org.Slug, p.Slug, a)
					if err != nil {
						continue
					}
					for _, v := range versions {
						ok, err := svc.DB.EnsureRelease(ctx, org.ID, p.ID, a, v, "artifacts/"+a+"/"+v)
						if err != nil {
							return err
						}
						if ok {
							repaired++
							fmt.Printf("repaired: release row %s %s in %s/%s\n", a, v, org.Slug, p.Slug)
						}
					}
				}
				branches, err := svc.Git.Branches(ctx, org.Slug, p.Slug)
				if err != nil {
					continue
				}
				have := map[string]bool{}
				for _, b := range branches {
					have[b] = true
				}
				open, err := svc.DB.ListProposals(ctx, p.ID, "open")
				if err != nil {
					return err
				}
				for _, pr := range open {
					if !have[pr.Branch] {
						orphans++
						fmt.Printf("ORPHAN: open proposal %s (%s) has no branch %s — reject it or restore the branch\n", pr.ID, pr.Artifact, pr.Branch)
					}
				}
			}
		}
		fmt.Printf("fsck done: %d release rows repaired, %d orphan proposals\n", repaired, orphans)
		return nil
	})
}
