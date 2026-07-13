package semver

import "testing"

func TestParseBumpCompare(t *testing.T) {
	v, err := Parse("v1.2.3")
	if err != nil || v != (Version{1, 2, 3}) {
		t.Fatalf("parse: %v %v", v, err)
	}
	for _, bad := range []string{"1.2.3", "v1.2", "v1.2.3-rc1", "va.b.c", ""} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
	cases := []struct{ kind, want string }{
		{"major", "v2.0.0"}, {"minor", "v1.3.0"}, {"patch", "v1.2.4"},
	}
	for _, c := range cases {
		got, err := Bump(v, c.kind)
		if err != nil || got.String() != c.want {
			t.Errorf("bump %s: got %v want %s (%v)", c.kind, got, c.want, err)
		}
	}
	if _, err := Bump(v, "huge"); err == nil {
		t.Error("invalid bump kind must error")
	}
	if Compare(Version{1, 2, 3}, Version{1, 10, 0}) != -1 {
		t.Error("numeric compare, not lexicographic")
	}
}

func TestLatest(t *testing.T) {
	best, ok := Latest([]string{"v0.1.0", "v0.10.0", "v0.2.9", "garbage"})
	if !ok || best.String() != "v0.10.0" {
		t.Fatalf("got %v %v", best, ok)
	}
	if _, ok := Latest([]string{"nope"}); ok {
		t.Fatal("no valid versions should return ok=false")
	}
}
