package enginetests

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestFTSRankUnknownResolution(t *testing.T) {
	for _, name := range []string{"ts_rank", "ts_rank_cd"} {
		base := uint32(3703)
		if name == "ts_rank_cd" {
			base = 3707
		}
		for _, tc := range []struct {
			name, args string
			oid        uint32
		}{
			{"unknown_three_conflict", "'a','a','0'", 0},
			{"unknown_two", "'cat:1','cat'", base + 3},
			{"known_vector_three", "'cat:1'::tsvector,'cat','0'", base + 2},
			{"known_weights_three", "'{0.1,0.2,0.4,1}'::real[],'cat:1','cat'", base + 1},
			{"unknown_four", "'{0.1,0.2,0.4,1}','cat:1','cat','0'", base},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) { ftsRankHeadlineUnknownResolution(t, name, tc.args, tc.oid) })
		}
	}
}

func TestFTSHeadlineUnknownResolution(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		oid        uint32
	}{
		// Every slot independently prefers string against the original two
		// text-only candidates. Neither satisfies all three preferences, so
		// released resolution retains both rather than choosing sequentially.
		{"unknown_three_zero_survivors", "'english','cat','cat'", 0},
		{"unknown_three_valid_options_still_ambiguous", "'Cat','cat','HighlightAll=true'", 0},
		{"known_configuration", "'english'::regconfig,'Cat','cat'", 3744},
		{"known_document", "'Cat'::text,'cat','HighlightAll=true'", 3754},
		{"known_query_second", "'Cat','cat'::tsquery,'HighlightAll=true'", 3754},
		{"known_query_third", "'english','Cat','cat'::tsquery", 3744},
		{"unknown_two", "'Cat','cat'", 3755},
		{"unknown_four", "'english','Cat','cat','HighlightAll=true'", 3743},
	} {
		t.Run(tc.name, func(t *testing.T) { ftsRankHeadlineUnknownResolution(t, "ts_headline", tc.args, tc.oid) })
	}
}

// Exercise ordinary binding only: no runtime options parsing, normalization,
// headline acquisition or score computation can decide an unresolved overload.
func ftsRankHeadlineUnknownResolution(t *testing.T, name, args string, wantOID uint32) {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, t.Context(), engine.New(), sqltype.InputEnvironment{}, nil)
	tracked := &ftsLookupTrackedBinder{Binder: binder}
	raw := enginetest.Statement(t, "SELECT "+name+"("+args+")").(*parser.Select).Targets[0].(parser.FunctionExpr)
	definition, ok := functions.Lookup(name)
	if !ok {
		t.Fatal(name)
	}
	node, err := definition.Bind(tracked, raw)
	if len(tracked.calls) != len(raw.Args) || !reflect.DeepEqual(tracked.calls, raw.Args) {
		t.Fatal("overload resolution reanalyzed or reordered arguments", tracked.calls, raw.Args)
	}
	if wantOID == 0 {
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42725", Message: fmt.Sprintf("function %s(unknown, unknown, unknown) is not unique", name), Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: "8"}
		var diagnostic *sqlstate.Error
		if node != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatal("unknown ambiguity/full binding diagnostic", node, err, want)
		}
		return
	}
	if err != nil || node == nil {
		t.Fatal("typed discriminator or unique form rejected", name, args, err)
	}
	info := expr.InfoOf(node)
	if info.CallRoutineReference() == nil || info.CallRoutineReference().RoutineOID() != wantOID {
		t.Fatal("incorrect original overload identity", wantOID, info)
	}
	if name == "ts_headline" {
		if _, ok := node.(functions.FTSHeadline); !ok {
			t.Fatalf("not the checked headline provider: %T", node)
		}
	} else {
		if _, ok := node.(functions.FTSRank); !ok {
			t.Fatalf("not the checked rank provider: %T", node)
		}
	}
}
