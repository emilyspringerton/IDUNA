package handlers_test

import (
	"strings"
	"testing"
)

// TestSocialFlow drives the real, full S536 lifecycle (profile, friend request, mutual-request
// auto-accept, friends list, duel challenge, decline, unfriend) through GameOnlineHandler's real
// ServeHTTP dispatch and real SQLite migrations -- not mocked.
func TestSocialFlow(t *testing.T) {
	e := newGameEnv(t)
	pidA, _, tokA := e.register(t, "deadweight", "Ada")
	pidB, _, tokB := e.register(t, "deadweight", "Bea")
	pidC, _, tokC := e.register(t, "deadweight", "Cid")

	// profile is public, no auth needed, and starts at zero stats / zero friends.
	code, m, _ := e.do("GET", "/api/v1/games/deadweight/players/"+pidA+"/profile", "", nil)
	if code != 200 || m["player_id"] != pidA || m["display_name"] != "Ada" || m["friend_count"].(float64) != 0 {
		t.Fatalf("profile: %d %v", code, m)
	}
	if c, _, _ := e.do("GET", "/api/v1/games/deadweight/players/no-such/profile", "", nil); c != 404 {
		t.Errorf("unknown profile should 404, got %d", c)
	}

	// cannot friend yourself
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/friend-requests", tokA, map[string]string{"to_player_id": pidA}); c != 400 {
		t.Errorf("self-friend should 400, got %d", c)
	}

	// A -> B friend request: pending, shows up in B's incoming and A's outgoing.
	code, m, raw := e.do("POST", "/api/v1/games/deadweight/friend-requests", tokA, map[string]string{"to_player_id": pidB})
	if code != 201 || m["status"] != "pending" {
		t.Fatalf("friend request A->B: %d %s", code, raw)
	}
	_, lb, _ := e.do("GET", "/api/v1/games/deadweight/friend-requests", tokB, nil)
	incoming := lb["incoming"].([]any)
	if len(incoming) != 1 {
		t.Fatalf("B incoming = %v, want 1", lb)
	}
	reqID := incoming[0].(map[string]any)["id"].(float64)
	_, la, _ := e.do("GET", "/api/v1/games/deadweight/friend-requests", tokA, nil)
	if len(la["outgoing"].([]any)) != 1 {
		t.Fatalf("A outgoing = %v, want 1", la)
	}

	// B declines a second, independent request from C first, to prove decline doesn't affect A.
	e.do("POST", "/api/v1/games/deadweight/friend-requests", tokC, map[string]string{"to_player_id": pidB})
	_, lb2, _ := e.do("GET", "/api/v1/games/deadweight/friend-requests", tokB, nil)
	var cReqID float64
	for _, x := range lb2["incoming"].([]any) {
		row := x.(map[string]any)
		if row["requester_id"] == pidC {
			cReqID = row["id"].(float64)
		}
	}
	code, m, _ = e.do("POST", "/api/v1/games/deadweight/friend-requests/"+itoa(int64(cReqID))+"/decline", tokB, nil)
	if code != 200 || m["status"] != "declined" {
		t.Fatalf("decline C->B: %d %v", code, m)
	}
	// a non-recipient (C) cannot accept/decline someone else's request.
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/friend-requests/"+itoa(int64(reqID))+"/accept", tokC, nil); c != 404 {
		t.Errorf("non-recipient accept should 404, got %d", c)
	}

	// B accepts A's request -- both become friends.
	code, m, _ = e.do("POST", "/api/v1/games/deadweight/friend-requests/"+itoa(int64(reqID))+"/accept", tokB, nil)
	if code != 200 || m["status"] != "accepted" {
		t.Fatalf("accept A<-B: %d %v", code, m)
	}

	// friend_count and the /friends list itself both reflect the new friendship.
	_, profA, _ := e.do("GET", "/api/v1/games/deadweight/players/"+pidA+"/profile", "", nil)
	if profA["friend_count"].(float64) != 1 {
		t.Fatalf("A friend_count after accept = %v, want 1", profA["friend_count"])
	}
	_, profB, _ := e.do("GET", "/api/v1/games/deadweight/players/"+pidB+"/profile", "", nil)
	if profB["friend_count"].(float64) != 1 {
		t.Fatalf("B friend_count after accept = %v, want 1", profB["friend_count"])
	}
	code, _, raw = e.do("GET", "/api/v1/games/deadweight/friends", tokA, nil)
	if code != 200 || !strings.Contains(string(raw), pidB) {
		t.Fatalf("A friends list = %d %s, want to contain B (%s)", code, raw, pidB)
	}

	// mutual simultaneous request (C -> A, then A -> C) auto-accepts instead of leaving two
	// dangling pending rows.
	e.do("POST", "/api/v1/games/deadweight/friend-requests", tokC, map[string]string{"to_player_id": pidA})
	code, m, _ = e.do("POST", "/api/v1/games/deadweight/friend-requests", tokA, map[string]string{"to_player_id": pidC})
	if code != 200 || m["status"] != "accepted" {
		t.Fatalf("mutual auto-accept C<->A: %d %v", code, m)
	}

	// duel: C is not a friend of B, so B cannot duel C.
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/duels", tokB, map[string]string{"to_player_id": pidC}); c != 403 {
		t.Errorf("duel between non-friends should 403, got %d", c)
	}
	// A and B are friends: A can challenge B.
	code, m, raw = e.do("POST", "/api/v1/games/deadweight/duels", tokA, map[string]string{"to_player_id": pidB})
	if code != 201 || m["status"] != "pending" {
		t.Fatalf("duel create: %d %s", code, raw)
	}
	duelID := m["id"].(float64)
	code, m, _ = e.do("POST", "/api/v1/games/deadweight/duels/"+itoa(int64(duelID))+"/accept", tokB, nil)
	if code != 200 || m["status"] != "accepted" {
		t.Fatalf("duel accept: %d %v", code, m)
	}
	// only the challenged player may respond.
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/duels/"+itoa(int64(duelID))+"/decline", tokA, nil); c != 404 {
		t.Errorf("challenger responding to own duel should 404, got %d", c)
	}

	// unfriend A/B, then A can no longer duel B.
	code, m, _ = e.do("DELETE", "/api/v1/games/deadweight/friends/"+pidB, tokA, nil)
	if code != 200 || m["status"] != "removed" {
		t.Fatalf("unfriend: %d %v", code, m)
	}
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/duels", tokA, map[string]string{"to_player_id": pidB}); c != 403 {
		t.Errorf("duel after unfriend should 403, got %d", c)
	}
}

