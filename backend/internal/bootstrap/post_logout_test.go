package bootstrap

import "testing"

func TestWithPostLogoutRedirect(t *testing.T) {
	const uri = "https://app.example.com/signin"
	cases := []struct {
		name, current, want string
		added               bool
	}{
		{"empty", "", uri, true},
		{"plus is not enough", "+", "+##" + uri, true},
		{"operator entry preserved", "https://other.example.com/out", "https://other.example.com/out##" + uri, true},
		{"already listed", "https://other.example.com/out##" + uri, "https://other.example.com/out##" + uri, false},
	}
	for _, tc := range cases {
		got, added := withPostLogoutRedirect(tc.current, uri)
		if got != tc.want || added != tc.added {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", tc.name, got, added, tc.want, tc.added)
		}
	}
}
