package functions

import (
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The hidden-window seam must report the ordinary signature without asking the
// analyzer to note a candidate for composite-field recovery.
type routineDropResolutionBinder struct {
	AggregateBinder
	noted bool
}

func (b *routineDropResolutionBinder) BuiltinVisible(parser.ObjectName) (bool, error) {
	return false, nil
}
func (b *routineDropResolutionBinder) AnalyzeAggregateFilter(parser.AggregateExpr) (expr.Expr, error) {
	return nil, nil
}
func (b *routineDropResolutionBinder) SignatureError(name parser.ObjectName, pos parser.Position, names []string) error {
	b.noted = true
	return ResolutionError(name, pos, names, false)
}

func TestRoutineDropUnnotedResolution(t *testing.T) {
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: "num_nulls"}}}
	pos := parser.Position{Character: 8, Offset: 7}
	assert := func(err error, code, message, hint string) {
		t.Helper()
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint, Position: "8"}
		if got, ok := err.(*sqlstate.Error); !ok || *got != want {
			t.Fatalf("got %+v, want %+v", err, want)
		}
	}
	assert(ResolutionError(name, pos, nil, false), "42883", "function public.num_nulls() does not exist", SignatureHint)
	assert(ResolutionError(name, pos, nil, true), "42725", "function public.num_nulls() is not unique", "Could not choose a best candidate function. You might need to add explicit type casts.")
	assert(sumSignatureError(name, pos, nil, 2), "42883", "function public.num_nulls() does not exist", "No aggregate function matches the given name and argument types. Perhaps you misplaced ORDER BY; ORDER BY must appear after all regular arguments of the aggregate.")
	assert(sumSignatureError(name, pos, nil, 0), "42883", "function public.num_nulls() does not exist", SignatureHint)

	b := &routineDropResolutionBinder{}
	windowName := parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: "row_number"}}}
	_, err := rowNumberSignature(b, parser.AggregateExpr{Name: "row_number", Pos: pos}, windowName)
	assert(err, "42883", "function public.row_number() does not exist", SignatureHint)
	if b.noted {
		t.Fatal("hidden window miss requested composite-field recovery")
	}
	// The separate public noted route still requests analyzer recovery.
	assert(NotedResolutionError(b, name, pos, nil, false), "42883", "function public.num_nulls() does not exist", SignatureHint)
	if !b.noted {
		t.Fatal("noted signature lost composite-field recovery")
	}
}
