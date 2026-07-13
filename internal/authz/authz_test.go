package authz

import "testing"

// The full role×action matrix, exhaustively (QA review: pin RBAC with a
// table; a new Action added without updating this test fails below).
func TestMatrix(t *testing.T) {
	allActions := []Action{
		ContextRead, ArtifactRead, ArtifactWrite, ProposalCreate, ProposalMerge,
		ReleaseCreate, MemoryRead, MemoryWrite, ActivityRead, ProjectAdmin,
	}
	allowed := map[Role]map[Action]bool{
		RoleNone: {},
		RoleViewer: {
			ContextRead: true, ArtifactRead: true, MemoryRead: true, ActivityRead: true,
		},
		RoleContributor: {
			ContextRead: true, ArtifactRead: true, MemoryRead: true, ActivityRead: true,
			ArtifactWrite: true, ProposalCreate: true, MemoryWrite: true,
		},
		RoleMaintainer: {
			ContextRead: true, ArtifactRead: true, MemoryRead: true, ActivityRead: true,
			ArtifactWrite: true, ProposalCreate: true, MemoryWrite: true,
			ProposalMerge: true, ReleaseCreate: true, ProjectAdmin: true,
		},
	}
	for role, wants := range allowed {
		for _, a := range allActions {
			d := Can(role, a)
			if wants[a] && d != nil {
				t.Errorf("role %q should allow %q, got denial %v", role, a, d)
			}
			if !wants[a] && d == nil {
				t.Errorf("role %q must NOT allow %q", role, a)
			}
		}
	}
	// Every action in the grants map is a known action (catches typos).
	known := map[Action]bool{}
	for _, a := range allActions {
		known[a] = true
	}
	for role, m := range grants {
		for a := range m {
			if !known[a] {
				t.Errorf("grants for %q reference unknown action %q", role, a)
			}
		}
	}
}

func TestDenialIsStructuredAndTeaches(t *testing.T) {
	d := Can(RoleContributor, ProposalMerge)
	if d == nil {
		t.Fatal("contributor must not merge")
	}
	if d.CallerRole != "contributor" || d.Alternative == "" {
		t.Fatalf("denial should carry role and alternative: %+v", d)
	}
	if Can(RoleNone, ContextRead) == nil {
		t.Fatal("no-role must be denied everything")
	}
}
