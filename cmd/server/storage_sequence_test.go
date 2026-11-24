package main

import "testing"

func TestStorageSequenceCommandRequiresExplicitRecovery(t *testing.T) {
	for _, args := range [][]string{nil, {"-org", "org-a"}, {"-past", "5"}, {"-org", "org-a", "-past", "-1"}, {"-org", "org-a", "-past", "9223372036854775807"}, {"-org", "org-a", "-past", "1", "extra"}} {
		if err := runAdvanceStorageSequence(args); err == nil {
			t.Fatalf("accepted invalid operator recovery: %v", args)
		}
	}
}
