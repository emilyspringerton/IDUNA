package promptoverse

import "testing"

func seedTwoLeafSubject(t *testing.T, s *Store, subject string) {
	t.Helper()
	for i, slug := range []string{subject + "-leaf-1", subject + "-leaf-2"} {
		if _, err := s.Create(Node{
			Slug: slugifyForTest(slug), Label: "style", Subject: subject, Kind: "surreal",
			EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png",
		}); err != nil {
			t.Fatalf("seed node %d: %v", i, err)
		}
	}
}

func slugifyForTest(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		case r == ' ' || r == '-' || r == '_':
			out = append(out, '-')
		}
	}
	return string(out)
}

func TestDistinctSubjects_OnlyReturnsSubjectsWithTwoOrMoreNodes(t *testing.T) {
	s := newTestStore(t)
	seedTwoLeafSubject(t, s, "Fractal")
	if _, err := s.Create(Node{Slug: "solo", Label: "style", Subject: "Solo Subject", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png"}); err != nil {
		t.Fatal(err)
	}

	subjects, err := s.DistinctSubjects()
	if err != nil {
		t.Fatalf("DistinctSubjects: %v", err)
	}
	if len(subjects) != 1 || subjects[0] != "Fractal" {
		t.Errorf("expected only [Fractal] (>=2 nodes), got %v", subjects)
	}
}

func TestCreateMashupNomination_Succeeds(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateMashupNomination("Fractal", "Raccoon", "user-1")
	if err != nil {
		t.Fatalf("CreateMashupNomination: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected a positive id, got %d", id)
	}

	nominations, err := s.ListMashupNominations("")
	if err != nil {
		t.Fatalf("ListMashupNominations: %v", err)
	}
	if len(nominations) != 1 {
		t.Fatalf("expected 1 nomination, got %d", len(nominations))
	}
	n := nominations[0]
	if n.SubjectA != "Fractal" || n.SubjectB != "Raccoon" || n.NominatedBy != "user-1" || n.Status != "pending" {
		t.Errorf("unexpected nomination: %+v", n)
	}
}

func TestCreateMashupNomination_DuplicatePairSameUserRejected(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateMashupNomination("Fractal", "Raccoon", "user-1"); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreateMashupNomination("Fractal", "Raccoon", "user-1")
	if err != ErrDuplicateNomination {
		t.Errorf("expected ErrDuplicateNomination, got %v", err)
	}
}

func TestCreateMashupNomination_SamePairDifferentUsersBothAllowed(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateMashupNomination("Fractal", "Raccoon", "user-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMashupNomination("Fractal", "Raccoon", "user-2"); err != nil {
		t.Errorf("expected a different user's identical nomination to be allowed, got %v", err)
	}
}

func TestCreateMashupNomination_TooManyPendingRejected(t *testing.T) {
	s := newTestStore(t)
	pairs := [][2]string{
		{"A", "B"}, {"C", "D"}, {"E", "F"}, {"G", "H"}, {"I", "J"},
	}
	for _, p := range pairs {
		if _, err := s.CreateMashupNomination(p[0], p[1], "user-1"); err != nil {
			t.Fatalf("expected the first %d nominations to succeed, got %v", maxPendingNominationsPerUser, err)
		}
	}
	_, err := s.CreateMashupNomination("K", "L", "user-1")
	if err != ErrTooManyPendingNominations {
		t.Errorf("expected ErrTooManyPendingNominations on the 6th pending nomination, got %v", err)
	}
}

func TestReviewMashupNomination_ApproveMarksStatusAndReviewer(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateMashupNomination("Fractal", "Raccoon", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReviewMashupNomination(id, "approved", "admin-1"); err != nil {
		t.Fatalf("ReviewMashupNomination: %v", err)
	}
	nominations, err := s.ListMashupNominations("approved")
	if err != nil {
		t.Fatal(err)
	}
	if len(nominations) != 1 || nominations[0].ReviewedBy != "admin-1" || nominations[0].ReviewedAt == nil {
		t.Errorf("unexpected nomination after review: %+v", nominations)
	}
}

func TestReviewMashupNomination_CannotReReviewAlreadyDecided(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateMashupNomination("Fractal", "Raccoon", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReviewMashupNomination(id, "approved", "admin-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReviewMashupNomination(id, "rejected", "admin-2"); err == nil {
		t.Error("expected an error re-reviewing an already-decided nomination")
	}
}

func TestReviewMashupNomination_UnknownIDFails(t *testing.T) {
	s := newTestStore(t)
	if err := s.ReviewMashupNomination(9999, "approved", "admin-1"); err == nil {
		t.Error("expected an error reviewing a nomination that doesn't exist")
	}
}

func TestListMashupNominations_FiltersByStatus(t *testing.T) {
	s := newTestStore(t)
	id1, _ := s.CreateMashupNomination("A", "B", "user-1")
	if _, err := s.CreateMashupNomination("C", "D", "user-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReviewMashupNomination(id1, "approved", "admin-1"); err != nil {
		t.Fatal(err)
	}

	pending, err := s.ListMashupNominations("pending")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].SubjectA != "C" {
		t.Errorf("expected only the still-pending nomination, got %+v", pending)
	}

	approved, err := s.ListMashupNominations("approved")
	if err != nil {
		t.Fatal(err)
	}
	if len(approved) != 1 || approved[0].SubjectA != "A" {
		t.Errorf("expected only the approved nomination, got %+v", approved)
	}
}
