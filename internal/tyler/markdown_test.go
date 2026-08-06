package tyler

import "testing"

func TestToHTML_Headers(t *testing.T) {
	got := toHTML("# One\n## Two\n### Three")
	want := "<h1>One</h1>\n<h2>Two</h2>\n<h3>Three</h3>\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestToHTML_BoldItalicCode(t *testing.T) {
	got := toHTML("**FROG (TEXT CARD)**\nI know the *secret* to `time travel`.")
	if !contains(got, "<strong>FROG (TEXT CARD)</strong>") {
		t.Errorf("missing bold: %q", got)
	}
	if !contains(got, "<em>secret</em>") {
		t.Errorf("missing italic: %q", got)
	}
	if !contains(got, "<code>time travel</code>") {
		t.Errorf("missing inline code: %q", got)
	}
	// Consecutive non-blank lines in the same block should become one <p>
	// joined by <br>, not two separate paragraphs -- this is what keeps a
	// character tag on its own line above the dialogue.
	if !contains(got, "<br>") {
		t.Errorf("expected line break preserved within paragraph: %q", got)
	}
}

func TestToHTML_Checklist(t *testing.T) {
	got := toHTML("- [x] Done thing\n- [ ] Not done thing")
	if !contains(got, `<input type="checkbox" disabled checked> Done thing`) {
		t.Errorf("checked item wrong: %q", got)
	}
	if !contains(got, `<input type="checkbox" disabled> Not done thing`) {
		t.Errorf("unchecked item wrong: %q", got)
	}
}

func TestToHTML_Table(t *testing.T) {
	got := toHTML("| Hero | Q |\n|---|---|\n| Frog | LOOP BACK |\n| Tree | VINE LASH |")
	for _, want := range []string{"<table>", "<th>Hero</th>", "<th>Q</th>", "<td>Frog</td>", "<td>LOOP BACK</td>"} {
		if !contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestToHTML_CodeFenceAndRule(t *testing.T) {
	got := toHTML("```\nUI OVERLAY\n```\n\n---\n\nAfter.")
	if !contains(got, "<pre>UI OVERLAY</pre>") {
		t.Errorf("missing code fence: %q", got)
	}
	if !contains(got, "<hr>") {
		t.Errorf("missing rule: %q", got)
	}
}

func TestToHTML_EscapesHTML(t *testing.T) {
	got := toHTML("Tags like <script> should not execute.")
	if contains(got, "<script>") {
		t.Errorf("raw HTML leaked through unescaped: %q", got)
	}
	if !contains(got, "&lt;script&gt;") {
		t.Errorf("expected escaped script tag: %q", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}
