package enginetests

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestInstalledNamedCallDeclaredFormals(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION citext")
	for _, tc := range []struct {
		source  string
		nominal bool
		order   [7]uint8
		arity   int
	}{
		{"SELECT regexp_match(pattern=>'a',string=>'AbA'::citext)", true, [7]uint8{1, 0}, 2},
		{"SELECT public.regexp_replace(flags=>'g'::text,replacement=>'X'::text,pattern=>'a'::citext,string=>'AbA'::citext)", true, [7]uint8{3, 2, 1, 0}, 4},
		{"SELECT pg_catalog.regexp_match(pattern=>'a',string=>'AbA'::text)", false, [7]uint8{1, 0}, 2},
		{"SELECT pg_catalog.regexp_replace(flags=>'g',replacement=>'X',pattern=>'a',string=>'AbA'::text)", false, [7]uint8{3, 2, 1, 0}, 4},
	} {
		t.Run(tc.source, func(t *testing.T) {
			raw := enginetest.Statement(t, tc.source).(*parser.Select).Targets[0]
			binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			bound, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			call, ok := bound.(functions.Regexp)
			if !ok || (call.Citext != nil) != tc.nominal || call.WrittenSlots != tc.order || len(call.Args) != tc.arity {
				t.Fatal("selected declaration or written formal order changed", bound)
			}
			for i, arg := range call.Args {
				wantNominal := tc.nominal && i < 2
				if arg.ResultUsage().Type.IsCitext() != wantNominal {
					t.Fatal("selected declaration lost nominal/text formal", i, arg.ResultUsage())
				}
			}
			if tc.nominal {
				view := e.CatalogRead()
				procedure := view.MustProcedure(call.Citext.Ref.Address)
				if call.RoutineReference != procedure || call.Citext.Ref.Domain() != view.Owner().Domain {
					t.Fatal("nominal selection lost genuine installed declaration")
				}
			}
		})
	}
}
