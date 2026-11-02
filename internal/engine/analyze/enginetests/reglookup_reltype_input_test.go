package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRegLookupRelTypeRetainedInput(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		source string
		result sqltype.ID
	}{
		{`SELECT to_regclass('pg_catalog.pg_class')`, sqltype.Regclass},
		{`SELECT to_regnamespace('pg_catalog')`, sqltype.Regnamespace},
		{`SELECT to_regtype('integer')`, sqltype.Regtype},
		{`SELECT to_regtypemod('varchar(12)')`, sqltype.Int4},
	} {
		t.Run(tc.source, func(t *testing.T) {
			b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			raw := enginetest.Statement(t, tc.source).(*parser.Select).Targets[0]
			node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			lookup, ok := node.(functions.RegLookup)
			if !ok || lookup.Validate() != nil || lookup.Usage.Type != b.Type(tc.result) {
				t.Fatal("missing genuine typed recipe", node)
			}
			if b.Input.Regtype == nil {
				t.Fatal("initial binding lacks type input, even for INT4 result")
			}
			for _, failure := range []error{nil, errors.New("current catalog input capture failed")} {
				captured := false
				err := analyze.CaptureRetainedExpressionInput(lookup,
					func() error { captured = true; return failure },
					func() error { t.Fatal("registry lookup demanded FTS parser"); return nil },
					func(*sqltype.Descriptor) error { t.Fatal("registry lookup demanded diagnostic naming"); return nil })
				if !captured || err != failure {
					t.Fatal("retained recipe lost current destination demand/error", captured, err)
				}
			}
			for _, control := range []expr.Expr{
				b.Literal(sqltype.String(b.Type(sqltype.Text), "varchar(12)"), parser.Position{}),
				b.Literal(sqltype.Null(b.Type(tc.result)), parser.Position{}),
			} {
				err := analyze.CaptureRetainedExpressionInput(control,
					func() error { t.Fatal("unrelated value demanded destination authority"); return nil },
					func() error { t.Fatal("unrelated value demanded FTS parser"); return nil },
					func(*sqltype.Descriptor) error { t.Fatal("unrelated builtin value demanded naming"); return nil })
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
