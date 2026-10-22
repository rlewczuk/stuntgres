package functions

import (
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Only the two diagnostic hooks are relevant to this producer seam. Calling a
// different Binder method is a test failure through the embedded nil interface.
type routineDropResolutionBinder struct {
	Binder
	recorded error
	noted    bool
}

func (b *routineDropResolutionBinder) RecordUnresolvedCallable(err error) { b.recorded = err }
func (b *routineDropResolutionBinder) SignatureError(name parser.ObjectName, pos parser.Position, names []string) error {
	b.noted = true
	return ResolutionError(name, pos, names, false)
}

func TestRoutineDropUnnotedResolution(t *testing.T) {
	b := &routineDropResolutionBinder{}
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: "num_nulls"}}}
	pos := parser.Position{Character: 8, Offset: 7}
	err := UnnotedResolutionError(b, name, pos, nil, false)
	if err != b.recorded || b.noted {
		t.Fatal("missing-call identity changed composite-recovery policy", err, b.recorded, b.noted)
	}
	d, ok := err.(*sqlstate.Error)
	if !ok || d.Code != "42883" || d.Message != "function public.num_nulls() does not exist" || d.Position != "8" || d.Hint != SignatureHint {
		t.Fatal("missing diagnostic changed", err)
	}
	missing := b.recorded
	ambiguous := UnnotedResolutionError(b, name, pos, nil, true)
	if ambiguous == missing || b.recorded != missing || b.noted {
		t.Fatal("ambiguity recorded as missing or recovery was noted")
	}
	if d, ok := ambiguous.(*sqlstate.Error); !ok || d.Code != "42725" {
		t.Fatal(ambiguous)
	}
	// Specialized misplaced-ORDER-BY diagnostics describe a family shape, not
	// an unresolved callable. Numeric42883 must not classify that boundary.
	b.recorded = nil
	shape := sumSignatureError(b, name, pos, nil, 2)
	if b.recorded != nil || b.noted {
		t.Fatal("aggregate shape was classified as missing")
	}
	if d, ok := shape.(*sqlstate.Error); !ok || d.Code != "42883" || d.Hint == SignatureHint {
		t.Fatal(shape)
	}
	plain := sumSignatureError(b, name, pos, nil, 0)
	if b.recorded != plain || b.noted {
		t.Fatal("ordinary aggregate miss lost its unnoted identity")
	}
}
