package clubs

import (
	"fmt"
	"testing"
)

// The section 2.1 table, written out independently of the bundles in authz.go (P = player, C = coach,
// M = manager, O = owner only). A node's string is the row key; the three booleans are player/coach/manager.
var wantMatrix = []struct {
	node          Node
	p, c, m, only bool // only: owner-only node (p/c/m ignored)
}{
	{NodeView, true, true, true, false},
	{NodeRosterView, true, true, true, false},
	{NodeFeedRead, true, true, true, false},
	{NodeFeedPost, true, true, true, false},
	{NodeFeedModerate, false, true, true, false},
	{NodeDecksRead, true, true, true, false},
	{NodeDecksFeature, false, true, true, false},
	{NodeAnalyticsView, true, true, true, false},
	{NodeTelemetryContrib, true, true, true, false},
	{NodeMembersInvite, false, true, true, false},
	{NodeMembersInviteElev, false, false, true, false},
	{NodeMembersRequests, false, true, true, false},
	{NodeMembersRemovePlay, false, true, true, false},
	{NodeMembersRemoveCoach, false, false, true, false},
	{NodeMembersRemoveMgr, false, false, true, false},
	{NodeMembersPromote, false, false, true, false},
	{NodeMembersDemote, false, false, true, false},
	{NodeSettingsEdit, false, false, true, false},
	{NodeVisibilitySet, false, false, true, false},
	{NodeAuditView, false, false, true, false},
	{NodeOwnerTransfer, false, false, false, true},
	{NodeDelete, false, false, false, true},
}

func TestMatrixExhaustive(t *testing.T) {
	if len(wantMatrix) != len(AllNodes) {
		t.Fatalf("catalog has %d nodes, test table %d", len(AllNodes), len(wantMatrix))
	}
	for _, w := range wantMatrix {
		for _, role := range []Role{RolePlayer, RoleCoach, RoleManager} {
			want := map[Role]bool{RolePlayer: w.p, RoleCoach: w.c, RoleManager: w.m}[role]
			if w.only {
				want = false
			}
			if got := Can(role, w.node); got != want {
				t.Errorf("Can(%s,%s)=%v want %v", role, w.node, got, want)
			}
			// non-owner never holds owner-only; owner manager does
			for _, owner := range []bool{false, true} {
				wantO := want
				if w.only {
					wantO = owner && role == RoleManager
				}
				if got := CanOwner(role, owner, w.node); got != wantO {
					t.Errorf("CanOwner(%s,owner=%v,%s)=%v want %v", role, owner, w.node, got, wantO)
				}
			}
		}
	}
	for _, n := range []Role{"", "admin", "Manager"} {
		for _, w := range wantMatrix {
			if Can(n, w.node) || CanOwner(n, true, w.node) {
				t.Errorf("unknown role %q must hold nothing (%s)", n, w.node)
			}
		}
	}
}

func TestPermsIsSupersetChain(t *testing.T) {
	sets := map[Role]map[string]bool{}
	for _, r := range []Role{RolePlayer, RoleCoach, RoleManager} {
		sets[r] = map[string]bool{}
		for _, p := range Perms(r, false) {
			sets[r][p] = true
		}
	}
	for p := range sets[RolePlayer] {
		if !sets[RoleCoach][p] {
			t.Errorf("coach missing player perm %s", p)
		}
	}
	for p := range sets[RoleCoach] {
		if !sets[RoleManager][p] {
			t.Errorf("manager missing coach perm %s", p)
		}
	}
	if len(Perms(RoleManager, true)) != len(Perms(RoleManager, false))+2 {
		t.Error("owner should add exactly the two owner-only nodes")
	}
	if len(Perms("", true)) != 0 {
		t.Error("unknown role has perms")
	}
}

func TestRank(t *testing.T) {
	if !(Rank(RoleManager) > Rank(RoleCoach) && Rank(RoleCoach) > Rank(RolePlayer) && Rank(RolePlayer) > Rank("")) {
		t.Fatal("rank order broken")
	}
	if Valid("owner") {
		t.Fatal("owner is a flag, not a role")
	}
}

type actCase struct {
	name        string
	actor       Role
	actorOwner  bool
	target      Role
	targetOwner bool
	a           Action
	want        string // "" = allowed, else denial code
}

func TestCanActOnTable(t *testing.T) {
	P, C, M := RolePlayer, RoleCoach, RoleManager
	rm := func(mc int) Action { return Action{Kind: KindRemove, ManagerCount: mc} }
	cr := func(to Role, mc int) Action { return Action{Kind: KindChangeRole, NewRole: to, ManagerCount: mc} }
	inv := func(r Role) Action { return Action{Kind: KindInvite, NewRole: r, ManagerCount: 1} }
	cases := []actCase{
		// ---- remove: rank gating
		{"coach removes player", C, false, P, false, rm(1), ""},
		{"coach removes coach", C, false, C, false, rm(1), CodeForbidden},
		{"coach removes manager", C, false, M, false, rm(2), CodeForbidden},
		{"player removes player", P, false, P, false, rm(1), CodeForbidden},
		{"player removes coach", P, false, C, false, rm(1), CodeForbidden},
		{"manager removes player", M, false, P, false, rm(1), ""},
		{"manager removes coach", M, false, C, false, rm(1), ""},
		{"manager removes another manager", M, false, M, false, rm(2), ""},
		{"manager removes owner", M, false, M, true, rm(2), CodeOwnerImmune},
		{"owner removes another manager", M, true, M, false, rm(2), ""},
		{"owner removes owner row", M, true, M, true, rm(2), CodeOwnerImmune},
		{"remove last manager", M, false, M, false, rm(1), CodeLastManager},
		{"unknown actor", "", false, P, false, rm(1), CodeForbidden},
		{"unknown target", M, false, "", false, rm(1), CodeForbidden},
		{"manager self-remove via remove", M, false, M, false, Action{Kind: KindRemove, Self: true, ManagerCount: 2}, ""},
		{"last manager self-remove via remove", M, false, M, false, Action{Kind: KindRemove, Self: true, ManagerCount: 1}, CodeLastManager},
		{"owner self-remove via remove", M, true, M, true, Action{Kind: KindRemove, Self: true, ManagerCount: 3}, CodeOwnerImmune},

		// ---- leave
		{"player leaves", P, false, P, false, Action{Kind: KindLeave, Self: true, ManagerCount: 1}, ""},
		{"coach leaves", C, false, C, false, Action{Kind: KindLeave, Self: true, ManagerCount: 1}, ""},
		{"manager leaves with peers", M, false, M, false, Action{Kind: KindLeave, Self: true, ManagerCount: 2}, ""},
		{"last manager leaves", M, false, M, false, Action{Kind: KindLeave, Self: true, ManagerCount: 1}, CodeLastManager},
		{"owner leaves", M, true, M, true, Action{Kind: KindLeave, Self: true, ManagerCount: 4}, CodeOwnerImmune},

		// ---- change role
		{"manager promotes player to coach", M, false, P, false, cr(C, 1), ""},
		{"manager promotes coach to manager", M, false, C, false, cr(M, 1), ""},
		{"manager promotes player to manager", M, false, P, false, cr(M, 1), ""},
		{"manager demotes coach to player", M, false, C, false, cr(P, 1), ""},
		{"manager demotes other manager", M, false, M, false, cr(C, 2), ""},
		{"demote last manager", M, false, M, false, cr(C, 1), CodeLastManager},
		{"demote owner", M, false, M, true, cr(C, 3), CodeOwnerImmune},
		{"owner demotes owner row", M, true, M, true, cr(C, 3), CodeOwnerImmune},
		{"promote owner (no-op) refused as owner immune", M, false, M, true, cr(M, 3), CodeOwnerImmune},
		{"self promote (manager)", M, false, M, false, Action{Kind: KindChangeRole, NewRole: C, Self: true, ManagerCount: 2}, CodeSelfChange},
		{"self promote (player)", P, false, P, false, Action{Kind: KindChangeRole, NewRole: M, Self: true, ManagerCount: 1}, CodeSelfChange},
		{"coach promotes player", C, false, P, false, cr(C, 1), CodeForbidden},
		{"coach demotes coach", C, false, C, false, cr(P, 1), CodeForbidden},
		{"player promotes player", P, false, P, false, cr(C, 1), CodeForbidden},
		{"same role", M, false, C, false, cr(C, 1), CodeNoChange},
		{"bad new role", M, false, C, false, cr("god", 1), CodeBadRole},
		{"unknown target role", M, false, "", false, cr(C, 1), CodeForbidden},
		{"unknown actor", "", false, P, false, cr(C, 1), CodeForbidden},

		// ---- invites
		{"player invites player", P, false, "", false, inv(P), CodeForbidden},
		{"coach invites player", C, false, "", false, inv(P), ""},
		{"coach invites coach", C, false, "", false, inv(C), CodeForbidden},
		{"coach invites manager", C, false, "", false, inv(M), CodeRoleTooHigh},
		{"manager invites player", M, false, "", false, inv(P), ""},
		{"manager invites coach", M, false, "", false, inv(C), ""},
		{"manager invites manager", M, false, "", false, inv(M), ""},
		{"bad invite role", M, false, "", false, inv("owner"), CodeBadRole},
		{"unknown actor invite", "", false, "", false, inv(P), CodeForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := CanActOn(c.actor, c.actorOwner, c.target, c.targetOwner, c.a)
			got := ""
			if d != nil {
				got = d.Code
			}
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
	if d := CanActOn(M, false, P, false, Action{Kind: "weird"}); d == nil || d.Code != CodeForbidden {
		t.Fatalf("unknown kind must be denied, got %v", d)
	}
}

// Brute-force property check across the whole role cross-product: no combination ever lets a non-manager
// touch a manager, and nobody ever acts on the owner or on themself where forbidden.
func TestCanActOnProperties(t *testing.T) {
	roles := []Role{RolePlayer, RoleCoach, RoleManager}
	for _, ar := range roles {
		for _, tr := range roles {
			for _, to := range []bool{false, true} {
				for mc := 0; mc <= 3; mc++ {
					name := fmt.Sprintf("%s>%s owner=%v mc=%d", ar, tr, to, mc)
					rem := CanActOn(ar, false, tr, to, Action{Kind: KindRemove, ManagerCount: mc})
					if rem == nil {
						if to {
							t.Errorf("%s: removed the owner", name)
						}
						if ar != RoleManager && Rank(tr) >= Rank(ar) {
							t.Errorf("%s: removed non-lower rank", name)
						}
						if tr == RoleManager && mc <= 1 {
							t.Errorf("%s: removed last manager", name)
						}
					}
					for _, nr := range roles {
						ch := CanActOn(ar, false, tr, to, Action{Kind: KindChangeRole, NewRole: nr, ManagerCount: mc})
						if ch == nil {
							if to {
								t.Errorf("%s->%s: changed owner", name, nr)
							}
							if Rank(nr) > Rank(ar) {
								t.Errorf("%s->%s: granted above own role", name, nr)
							}
							if tr == RoleManager && nr != RoleManager && mc <= 1 {
								t.Errorf("%s->%s: demoted last manager", name, nr)
							}
							if ar != RoleManager {
								t.Errorf("%s->%s: non-manager changed a role", name, nr)
							}
						}
					}
				}
			}
		}
	}
}
