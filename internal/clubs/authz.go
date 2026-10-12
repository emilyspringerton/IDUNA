// Package clubs is the pure, dependency-free authorization core for WOTAN Clubs
// (WOTAN/CLUBS_NORTHSTAR.md section 2): role ranks, the permission-node matrix as data, and the
// hierarchy invariants of section 2.2 in one function, CanActOn.
//
// STOPGAP: PARENA-first. The monorepo standing rule is that core deps are implemented in PARENA first;
// this authz table is plain Go as a labelled stopgap so the clubs handler can ship now (same precedent as
// internal/hsdeck). It is deliberately free of I/O, database and net/http imports so a PARENA port is a
// mechanical translation of the matrix + CanActOn. Tracked as a PARENA replacement item.
package clubs

import "sort"

// Role is an in-club role. Roles are named bundles of permission nodes; ownership is a flag on the club.
type Role string

const (
	RolePlayer  Role = "player"
	RoleCoach   Role = "coach"
	RoleManager Role = "manager"
)

// Rank orders roles: manager 3 > coach 2 > player 1. Unknown or empty roles rank 0 (no standing).
func Rank(r Role) int {
	switch r {
	case RolePlayer:
		return 1
	case RoleCoach:
		return 2
	case RoleManager:
		return 3
	}
	return 0
}

// Valid reports whether r is one of the three real roles.
func Valid(r Role) bool { return Rank(r) > 0 }

// Node is a dotted, club-scoped permission node (section 2.1).
type Node string

const (
	NodeView               Node = "club.view"
	NodeRosterView         Node = "club.roster.view"
	NodeFeedRead           Node = "club.feed.read"
	NodeFeedPost           Node = "club.feed.post"
	NodeFeedModerate       Node = "club.feed.moderate"
	NodeDecksRead          Node = "club.decks.read"
	NodeDecksFeature       Node = "club.decks.feature"
	NodeAnalyticsView      Node = "club.analytics.view"
	NodeTelemetryContrib   Node = "club.telemetry.contribute"
	NodeMembersInvite      Node = "club.members.invite"
	NodeMembersInviteElev  Node = "club.members.invite.elevated"
	NodeMembersRequests    Node = "club.members.requests"
	NodeMembersRemovePlay  Node = "club.members.remove.player"
	NodeMembersRemoveCoach Node = "club.members.remove.coach"
	NodeMembersRemoveMgr   Node = "club.members.remove.manager"
	NodeMembersPromote     Node = "club.members.promote"
	NodeMembersDemote      Node = "club.members.demote"
	NodeSettingsEdit       Node = "club.settings.edit"
	NodeVisibilitySet      Node = "club.visibility.set"
	NodeAuditView          Node = "club.audit.view"
	// Owner-only nodes are not part of any role bundle: they are held by the club owner flag alone.
	NodeOwnerTransfer Node = "club.owner.transfer"
	NodeDelete        Node = "club.delete"
)

// AllNodes lists every node in the catalog, in spec order (role-bundled nodes first, then owner-only).
var AllNodes = []Node{
	NodeView, NodeRosterView, NodeFeedRead, NodeFeedPost, NodeFeedModerate, NodeDecksRead, NodeDecksFeature,
	NodeAnalyticsView, NodeTelemetryContrib, NodeMembersInvite, NodeMembersInviteElev, NodeMembersRequests,
	NodeMembersRemovePlay, NodeMembersRemoveCoach, NodeMembersRemoveMgr, NodeMembersPromote, NodeMembersDemote,
	NodeSettingsEdit, NodeVisibilitySet, NodeAuditView, NodeOwnerTransfer, NodeDelete,
}

var playerNodes = []Node{
	NodeView, NodeRosterView, NodeFeedRead, NodeFeedPost, NodeDecksRead, NodeAnalyticsView, NodeTelemetryContrib,
}

var coachExtra = []Node{
	NodeFeedModerate, NodeDecksFeature, NodeMembersInvite, NodeMembersRequests, NodeMembersRemovePlay,
}

var managerExtra = []Node{
	NodeMembersInviteElev, NodeMembersRemoveCoach, NodeMembersRemoveMgr, NodeMembersPromote, NodeMembersDemote,
	NodeSettingsEdit, NodeVisibilitySet, NodeAuditView,
}

// matrix is the section 2.1 table as data: role -> set of nodes. Built once from the bundles above so a
// higher role is always a strict superset of the one below it.
var matrix = func() map[Role]map[Node]bool {
	m := map[Role]map[Node]bool{RolePlayer: {}, RoleCoach: {}, RoleManager: {}}
	for _, n := range playerNodes {
		m[RolePlayer][n], m[RoleCoach][n], m[RoleManager][n] = true, true, true
	}
	for _, n := range coachExtra {
		m[RoleCoach][n], m[RoleManager][n] = true, true
	}
	for _, n := range managerExtra {
		m[RoleManager][n] = true
	}
	return m
}()

// Can reports whether role's bundle contains node. Owner-only nodes are never in a bundle; use CanOwner.
func Can(role Role, node Node) bool { return matrix[role][node] }

// IsOwnerOnly reports whether node is held by the club owner flag alone.
func IsOwnerOnly(node Node) bool { return node == NodeOwnerTransfer || node == NodeDelete }

// CanOwner is Can plus the owner-only nodes: the owner is always a manager, and additionally holds
// club.owner.transfer and club.delete. A non-owner never holds an owner-only node.
func CanOwner(role Role, isOwner bool, node Node) bool {
	if IsOwnerOnly(node) {
		return isOwner && role == RoleManager
	}
	return Can(role, node)
}

// Perms returns the effective node list for a member, sorted. Unknown role yields an empty list.
func Perms(role Role, isOwner bool) []string {
	out := []string{}
	for _, n := range AllNodes {
		if CanOwner(role, isOwner, n) {
			out = append(out, string(n))
		}
	}
	sort.Strings(out)
	return out
}

// ---- hierarchy invariants (section 2.2) ---------------------------------------------------------------

// Kind is the type of membership-affecting action being authorized.
type Kind string

const (
	KindRemove     Kind = "remove"      // remove another member
	KindChangeRole Kind = "change_role" // promote or demote (Action.NewRole is the role to set)
	KindLeave      Kind = "leave"       // actor removes themself
	KindInvite     Kind = "invite"      // mint an invite granting Action.NewRole
)

// Action carries everything beyond (actor, target) the invariants need.
type Action struct {
	Kind         Kind
	NewRole      Role // KindChangeRole: the role to set; KindInvite: the role the invite grants
	Self         bool // actor and target are the same subject
	ManagerCount int  // number of managers in the club right now (re-read inside the transaction)
}

// Denial codes. The handler maps these to HTTP statuses; none of them leak across clubs.
const (
	CodeForbidden   = "forbidden"     // missing node or insufficient rank
	CodeOwnerImmune = "owner_immune"  // the owner can only change via transfer or a platform-admin action
	CodeLastManager = "last_manager"  // would leave the club with no manager
	CodeSelfChange  = "self_change"   // nobody changes their own role
	CodeRoleTooHigh = "role_too_high" // cannot grant a role above your own
	CodeNoChange    = "no_change"     // role change to the same role
	CodeBadRole     = "bad_role"      // unknown role value
)

// Denial is the reason an action was refused. A nil *Denial means allowed.
type Denial struct {
	Code string
	Msg  string
}

func (d *Denial) Error() string { return d.Msg }

func deny(code, msg string) *Denial { return &Denial{Code: code, Msg: msg} }

// removeNode is the node required to remove a target of the given role.
func removeNode(target Role) Node {
	switch target {
	case RoleManager:
		return NodeMembersRemoveMgr
	case RoleCoach:
		return NodeMembersRemoveCoach
	}
	return NodeMembersRemovePlay
}

// CanActOn is the single function that enforces every section 2.2 invariant:
//
//  1. an actor may only remove/demote a target of strictly lower rank, except managers acting on other
//     managers (never the owner);
//  2. a coach can never touch another coach or a manager;
//  3. nobody grants a role above their own; coach invites are capped at player;
//  4. the last manager cannot be removed, demoted or leave; the owner changes only via transfer;
//  5. nobody changes their own role.
//
// It returns nil when allowed. The caller must re-read actorRole/targetRole/ManagerCount inside the same
// transaction as the mutation (invariant 6).
func CanActOn(actorRole Role, actorIsOwner bool, targetRole Role, targetIsOwner bool, a Action) *Denial {
	if !Valid(actorRole) {
		return deny(CodeForbidden, "not a member of this club")
	}
	switch a.Kind {
	case KindInvite:
		if !Valid(a.NewRole) {
			return deny(CodeBadRole, "unknown role")
		}
		if !Can(actorRole, NodeMembersInvite) {
			return deny(CodeForbidden, "your role cannot create invites")
		}
		if Rank(a.NewRole) > Rank(actorRole) {
			return deny(CodeRoleTooHigh, "you cannot invite someone above your own role")
		}
		if Rank(a.NewRole) > Rank(RolePlayer) && !Can(actorRole, NodeMembersInviteElev) {
			return deny(CodeForbidden, "your role can only invite players")
		}
		return nil

	case KindLeave:
		if targetIsOwner {
			return deny(CodeOwnerImmune, "the owner must transfer ownership before leaving")
		}
		if targetRole == RoleManager && a.ManagerCount <= 1 {
			return deny(CodeLastManager, "a club must keep at least one manager")
		}
		return nil

	case KindRemove:
		if a.Self {
			return CanActOn(actorRole, actorIsOwner, targetRole, targetIsOwner, Action{Kind: KindLeave, ManagerCount: a.ManagerCount})
		}
		if !Valid(targetRole) {
			return deny(CodeForbidden, "no such member")
		}
		if targetIsOwner {
			return deny(CodeOwnerImmune, "the owner cannot be removed")
		}
		if !Can(actorRole, removeNode(targetRole)) {
			return deny(CodeForbidden, "your role cannot remove this member")
		}
		if err := rankGate(actorRole, targetRole); err != nil {
			return err
		}
		if targetRole == RoleManager && a.ManagerCount <= 1 {
			return deny(CodeLastManager, "a club must keep at least one manager")
		}
		return nil

	case KindChangeRole:
		if a.Self {
			return deny(CodeSelfChange, "you cannot change your own role")
		}
		if !Valid(a.NewRole) {
			return deny(CodeBadRole, "unknown role")
		}
		if !Valid(targetRole) {
			return deny(CodeForbidden, "no such member")
		}
		if targetIsOwner {
			return deny(CodeOwnerImmune, "the owner's role can only change through ownership transfer")
		}
		if a.NewRole == targetRole {
			return deny(CodeNoChange, "member already has that role")
		}
		if Rank(a.NewRole) > Rank(targetRole) {
			if !Can(actorRole, NodeMembersPromote) {
				return deny(CodeForbidden, "your role cannot promote members")
			}
		} else if !Can(actorRole, NodeMembersDemote) {
			return deny(CodeForbidden, "your role cannot demote members")
		}
		if Rank(a.NewRole) > Rank(actorRole) {
			return deny(CodeRoleTooHigh, "you cannot grant a role above your own")
		}
		if err := rankGate(actorRole, targetRole); err != nil {
			return err
		}
		if targetRole == RoleManager && Rank(a.NewRole) < Rank(RoleManager) && a.ManagerCount <= 1 {
			return deny(CodeLastManager, "a club must keep at least one manager")
		}
		return nil
	}
	return deny(CodeForbidden, "unknown action")
}

// rankGate: the actor must outrank the target, except a manager may act on another (non-owner) manager.
func rankGate(actor, target Role) *Denial {
	if Rank(actor) > Rank(target) {
		return nil
	}
	if actor == RoleManager && target == RoleManager {
		return nil
	}
	return deny(CodeForbidden, "you can only act on members below your own role")
}
