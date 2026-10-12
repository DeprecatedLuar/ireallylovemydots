package manifest

import "testing"

func TestCheckName(t *testing.T) {
	cases := []struct {
		m       Manifest
		root    bool
		wantErr bool
	}{
		{Manifest{Name: "nvim"}, true, false},
		{Manifest{}, true, true},
		{Manifest{Name: "list"}, true, true},
		{Manifest{Name: "nvim"}, false, true},
		{Manifest{}, false, false},
	}
	for _, c := range cases {
		if err := CheckName(c.m, c.root); (err != nil) != c.wantErr {
			t.Errorf("CheckName(%+v, root=%v) = %v, wantErr %v", c.m, c.root, err, c.wantErr)
		}
	}
}
