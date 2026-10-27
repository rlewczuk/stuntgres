package enginetests_test

import (
	"stuntgres/internal/engine/catalog"
	"testing"
)

func TestRuleModeSelection(t *testing.T) {
	for _, tc := range []struct {
		mode                   catalog.RuleMode
		origin, local, replica bool
		code                   string
	}{
		{catalog.RuleOrigin, true, true, false, "O"}, {catalog.RuleDisabled, false, false, false, "D"}, {catalog.RuleReplica, false, false, true, "R"}, {catalog.RuleAlways, true, true, true, "A"},
	} {
		if !tc.mode.Valid() || tc.mode.String() != tc.code {
			t.Fatal(tc)
		}
		for _, role := range []struct{ replica, want bool }{{false, tc.origin}, {false, tc.local}, {true, tc.replica}} {
			if got := tc.mode.Fires(role.replica); got != role.want {
				t.Fatal(tc.mode, role, got)
			}
		}
	}
	for _, mode := range []catalog.RuleMode{0, 'x', 255} {
		if mode.Valid() || mode.String() != "" || mode.Fires(false) || mode.Fires(true) {
			t.Fatal("invalid mode acquired firing authority", mode)
		}
	}
}
