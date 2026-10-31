package isureal

import "testing"

func TestLikeContainsPattern(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "abc", want: "%abc%"},
		{in: "100%", want: `%100\%%`},
		{in: "a_b", want: `%a\_b%`},
		{in: `a\b`, want: `%a\\b%`},
		{in: `100%_a\`, want: `%100\%\_a\\%`},
	}
	for _, test := range tests {
		if got := likeContainsPattern(test.in); got != test.want {
			t.Errorf("likeContainsPattern(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}
