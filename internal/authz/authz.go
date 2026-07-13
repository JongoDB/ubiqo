// Package authz is ubiqo's RBAC kernel: a constant role→action matrix
// behind one function (ADR-0005). Deny-by-default; callers never branch on
// roles directly. Denials are structured so agents learn the protocol from
// the error itself.
package authz

import "fmt"

// Action names are stable API; MCP tools declare exactly one.
type Action string

const (
	ContextRead    Action = "context.read"
	ArtifactRead   Action = "artifact.read"
	ArtifactWrite  Action = "artifact.write"
	ProposalCreate Action = "proposal.create"
	ProposalMerge  Action = "proposal.merge"
	ReleaseCreate  Action = "release.create"
	MemoryRead     Action = "memory.read"
	MemoryWrite    Action = "memory.write"
	ActivityRead   Action = "activity.read"
	ProjectAdmin   Action = "project.admin"
)

// Project roles.
type Role string

const (
	RoleNone        Role = ""
	RoleViewer      Role = "viewer"
	RoleContributor Role = "contributor"
	RoleMaintainer  Role = "maintainer"
)

// Org roles (govern org-level operations, checked in the service layer).
type OrgRole string

const (
	OrgNone   OrgRole = ""
	OrgGuest  OrgRole = "guest"
	OrgMember OrgRole = "member"
	OrgAdmin  OrgRole = "admin"
	OrgOwner  OrgRole = "owner"
)

var grants = map[Role]map[Action]bool{
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

// alternative maps a denied action to the protocol step the caller CAN take.
var alternative = map[Action]string{
	ProposalMerge: "call ubiqo_propose_merge and ask a maintainer to merge",
	ReleaseCreate: "ask a maintainer to release, or request the maintainer role",
	ArtifactWrite: "ask for the contributor role on this project",
	ProjectAdmin:  "ask an org admin or project maintainer",
}

// Denial is the structured "no" (ADR-0005 / UX review): it teaches the next
// step instead of dead-ending the agent.
type Denial struct {
	Reason      string `json:"reason"`
	CallerRole  string `json:"caller_role"`
	Alternative string `json:"allowed_alternative,omitempty"`
}

func (d *Denial) Error() string {
	if d.Alternative != "" {
		return fmt.Sprintf("permission denied: %s (your role: %s) — %s", d.Reason, d.CallerRole, d.Alternative)
	}
	return fmt.Sprintf("permission denied: %s (your role: %s)", d.Reason, d.CallerRole)
}

// Can returns nil when role permits action, else a structured Denial.
func Can(role Role, action Action) *Denial {
	if grants[role][action] {
		return nil
	}
	roleName := string(role)
	if role == RoleNone {
		roleName = "no role on this project"
	}
	return &Denial{
		Reason:      fmt.Sprintf("action %q requires a role that grants it", action),
		CallerRole:  roleName,
		Alternative: alternative[action],
	}
}

// ValidRole reports whether s is an assignable project role.
func ValidRole(s string) bool {
	switch Role(s) {
	case RoleViewer, RoleContributor, RoleMaintainer:
		return true
	}
	return false
}

// ValidOrgRole reports whether s is an assignable org role.
func ValidOrgRole(s string) bool {
	switch OrgRole(s) {
	case OrgGuest, OrgMember, OrgAdmin, OrgOwner:
		return true
	}
	return false
}

// OrgCanManage reports whether an org role may administer org resources
// (create projects, manage members and devices).
func OrgCanManage(r OrgRole) bool { return r == OrgAdmin || r == OrgOwner }
