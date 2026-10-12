package middleware

import "testing"

func TestIsClubContextToken(t *testing.T) {
	cases := []struct {
		name   string
		claims map[string]any
		want   bool
	}{
		{"club string aud", map[string]any{"aud": "wotan-club"}, true},
		{"club array aud", map[string]any{"aud": []any{"x", "wotan-club"}}, true},
		{"other aud", map[string]any{"aud": "iduna"}, false},
		{"no aud", map[string]any{"sub": "u"}, false},
	}
	for _, c := range cases {
		if got := isClubContextToken(c.claims); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestIsNonIdentityToken(t *testing.T) {
	cases := []struct {
		name   string
		claims map[string]any
		want   bool
	}{
		{"club", map[string]any{"aud": "wotan-club"}, true},
		{"upload", map[string]any{"aud": "hs-live-upload", "sub": "u"}, true},
		{"upload array", map[string]any{"aud": []any{"x", "hs-live-upload"}}, true},
		{"oauth state", map[string]any{"aud": "iduna-oauth-state"}, true},
		{"normal player", map[string]any{"aud": "shankpit", "sub": "u"}, false},
		{"no aud", map[string]any{"sub": "u"}, false},
	}
	for _, c := range cases {
		if got := isNonIdentityToken(c.claims); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
