package main

import "testing"

func TestParseFlags(t *testing.T) {
	f, pos := parseFlags([]string{`D:\Game`, "--name", "game", "--count", "8", "--force"})
	if len(pos) != 1 || pos[0] != `D:\Game` {
		t.Fatalf("positional = %v", pos)
	}
	if f.str("name") != "game" {
		t.Errorf("name = %q", f.str("name"))
	}
	if f.intv("count", 0) != 8 {
		t.Errorf("count = %d", f.intv("count", 0))
	}
	if !f.has("force") || f["force"] != "true" {
		t.Errorf("force flag = %q", f["force"])
	}
}

func TestParseFlagsBareFlagAtEnd(t *testing.T) {
	f, _ := parseFlags([]string{"--verbose"})
	if !f.has("verbose") {
		t.Error("verbose flag missing")
	}
}

func TestFlagFloatAndDefault(t *testing.T) {
	f, _ := parseFlags([]string{"--size-gb", "2.5"})
	if f.float("size-gb", 0) != 2.5 {
		t.Errorf("size-gb = %v", f.float("size-gb", 0))
	}
	if f.float("missing", 7) != 7 {
		t.Errorf("default not used")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{36 * 1024 * 1024, "36.0 MB"},
		{2 * 1024 * 1024 * 1024, "2.0 GB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.n); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestNzYnTrim(t *testing.T) {
	if nz("", "def") != "def" || nz("x", "def") != "x" {
		t.Error("nz")
	}
	if yn(true, "y", "n") != "y" || yn(false, "y", "n") != "n" {
		t.Error("yn")
	}
	if trimLine("  a \r\n") != "a" {
		t.Error("trimLine")
	}
}
