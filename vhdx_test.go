package main

import "testing"

func TestParseKV(t *testing.T) {
	out := "noise line\nMOUNT=E:\nmore noise\n"
	if got := parseKV(out, "MOUNT="); got != "E:" {
		t.Errorf("parseKV = %q, want E:", got)
	}
	if got := parseKV("nothing here", "MOUNT="); got != "" {
		t.Errorf("parseKV(no match) = %q, want empty", got)
	}
}
