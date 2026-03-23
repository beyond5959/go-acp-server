package acpcli

import "testing"

func TestPickPermissionOptionIDNormalizesKinds(t *testing.T) {
	options := []PermissionOption{
		{OptionID: "allow-once", Kind: "allow-once"},
		{OptionID: "reject_always", Kind: "reject_always"},
	}

	if got, want := PickPermissionOptionID(options, "allow_once"), "allow-once"; got != want {
		t.Fatalf("PickPermissionOptionID(allow_once) = %q, want %q", got, want)
	}
	if got, want := PickPermissionOptionID(options, "reject-always"), "reject_always"; got != want {
		t.Fatalf("PickPermissionOptionID(reject-always) = %q, want %q", got, want)
	}
}
