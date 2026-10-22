package engine

import (
	"context"
	"testing"

	"stuntgres/internal/sqltype"
)

// Keep the actual exclusion consumer invariant, not a second inventory of
// admitted providers that must change whenever a scalar becomes executable.
func TestPGOperatorExclusionPredicate(t *testing.T) {
	var inet *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Inet {
			inet = d
			break
		}
	}
	if inet == nil {
		t.Fatal("missing INET descriptor")
	}
	class, ok := sqltype.LookupGiSTIndexClass("inet_ops")
	if !ok {
		t.Fatal("missing INET exclusion class")
	}
	operator, err := operatorFamilyTestExclusion(inet.Usage(), class, "&&")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	left, err := inet.Input(ctx, "10.0.0.1/24", -1)
	if err != nil {
		t.Fatal(err)
	}
	right, err := inet.Input(ctx, "10.0.0.2", -1)
	if err != nil {
		t.Fatal(err)
	}
	match, err := operator.Matches(ctx, left, right)
	if err != nil || !match {
		t.Fatal("exclusion overlap evaluation", match, err)
	}
}
