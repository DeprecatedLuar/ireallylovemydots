package git

import "testing"

func TestRealPath(t *testing.T) {
	hash := "3091b796b6ede9c7c5c5ca9267ac0350f15053fe"
	cases := map[string]string{
		"ns/a.conf~" + hash: "ns/a.conf",
		"ns/a.conf":         "ns/a.conf",
		"ns/backup~old":     "ns/backup~old",
		"ns/a~" + hash[:7]:  "ns/a~" + hash[:7],
	}
	for in, want := range cases {
		if got := realPath(in); got != want {
			t.Errorf("realPath(%q) = %q, want %q", in, got, want)
		}
	}
}
