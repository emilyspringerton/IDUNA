package backlog

import "testing"

const sample = `# EMILY BACKLOG

## SECTION 232: GFD/MINECRAFT CHAT BRIDGE BUG + IDUNA KANBAN NAV (2026-09-02)

Founder real-time, two parts: "so the chat bridge works from minecraft..."

- [ ] **S232-01: GFD↔EINHORN_SURVIVAL chat bridge is one-directional — logged, not fixed.**
  Founder-confirmed: Minecraft chat reaches the GFD (DragonsNShit) GUI client; typing in that
  client does not reach Minecraft. Traced (not fixed) to a real, likely root cause.
- [x] **S232-02: surfaced the already-built IDUNA kanban interface in the Back Office nav.**
  Added one link to the shared nav.

## SECTION 233: PRESS-RELEASE PROVIDER/SUBTYPE (2026-09-02)

- [x] **S233-01: real SourceProvider field, end to end.** Fixed a real bug.
- [ ] **S233-04: "fix" — both real HTTP bugs fixed, webdriver now works end to end against a
  real browser.** Founder's one-word follow-up to S233-03's diagnosis.
`

func TestParse_RealShapeMultiLineTitle(t *testing.T) {
	items := Parse(sample)
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d: %+v", len(items), items)
	}

	got := items[0]
	if got.ID != "S232-01" {
		t.Errorf("ID = %q, want S232-01", got.ID)
	}
	if got.Checked {
		t.Errorf("S232-01 should be unchecked")
	}
	if got.Section != 232 {
		t.Errorf("Section = %d, want 232", got.Section)
	}
	wantTitle := "GFD↔EINHORN_SURVIVAL chat bridge is one-directional — logged, not fixed."
	if got.Title != wantTitle {
		t.Errorf("Title = %q, want %q", got.Title, wantTitle)
	}

	if !items[1].Checked {
		t.Errorf("S232-02 should be checked")
	}

	// S233-04's own real, multi-line bold title (the exact shape that
	// motivated the (?s) non-greedy regex in the first place).
	last := items[3]
	if last.ID != "S233-04" {
		t.Fatalf("ID = %q, want S233-04", last.ID)
	}
	if last.Section != 233 {
		t.Errorf("Section = %d, want 233", last.Section)
	}
	wantLast := `"fix" — both real HTTP bugs fixed, webdriver now works end to end against a real browser.`
	if last.Title != wantLast {
		t.Errorf("Title = %q, want %q", last.Title, wantLast)
	}
}

func TestParse_NoSectionHeadingYieldsZero(t *testing.T) {
	items := Parse("- [ ] **S1-01: no section above this line.**\n")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Section != 0 {
		t.Errorf("Section = %d, want 0 (no heading seen yet)", items[0].Section)
	}
}

func TestByID(t *testing.T) {
	items := Parse(sample)
	idx := ByID(items)
	if _, ok := idx["S232-01"]; !ok {
		t.Fatalf("expected S232-01 in index")
	}
	if idx["S233-01"].Title == "" {
		t.Errorf("expected a real title for S233-01")
	}
	if _, ok := idx["S999-99"]; ok {
		t.Errorf("did not expect a match for a nonexistent id")
	}
}

func TestParseFile_MissingFileIsRealError(t *testing.T) {
	if _, err := ParseFile("/nonexistent/path/BACKLOG.md"); err == nil {
		t.Errorf("expected a real error for a missing file, got nil")
	}
}
