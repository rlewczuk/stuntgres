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

func TestRegLookupCollRoleRetainedInput(t *testing.T) {
	e := engine.New()
	for _, source := range []string{`SELECT to_regcollation('pg_catalog."C"')`, `SELECT to_regrole('pg_monitor')`} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, source).(*parser.Select).Targets[0]
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		lookup, ok := node.(functions.RegLookup)
		if !ok || lookup.Validate() != nil {
			t.Fatal("missing real retained recipe", node)
		}
		for _, failure := range []error{nil, errors.New("catalog input capture failed")} {
			captured := false
			err := analyze.CaptureRetainedExpressionInput(lookup,
				func() error { captured = true; return failure },
				func() error { t.Fatal("registry lookup demanded an FTS parser"); return nil },
				func(*sqltype.Descriptor) error { t.Fatal("builtin registry lookup demanded diagnostic type naming"); return nil })
			if !captured || err != failure {
				t.Fatal("retained lookup lost destination authority/capture error", source, captured, err)
			}
		}
		// Neither a TEXT value nor an unrelated alias result requests lookup
		// authority. The retained-input rule belongs to the recipe, not its type.
		for _, control := range []expr.Expr{
			b.Literal(sqltype.String(b.Type(sqltype.Text), "pg_monitor"), parser.Position{}),
			b.Literal(sqltype.Null(lookup.Usage.Type), parser.Position{}),
		} {
			err := analyze.CaptureRetainedExpressionInput(control,
				func() error { t.Fatal("unrelated value demanded catalog input"); return nil },
				func() error { t.Fatal("unrelated value demanded an FTS parser"); return nil },
				func(*sqltype.Descriptor) error { t.Fatal("unrelated builtin value demanded diagnostic naming"); return nil })
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
