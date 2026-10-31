package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestIdentifierArrayAnnotationAdmission(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	for _, typ := range []string{"xid", "cid", "xid8"} {
		for _, input := range []string{"NULL", "'{}'", "$1"} {
			parsed, err := parser.Parse("SELECT " + input + "::pg_catalog." + typ + "[]")
			if err != nil {
				t.Fatal(err)
			}
			b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			_, err = b.Bind(parsed[0].(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "unsupported column type" {
				t.Fatal("identifier array annotation must reject before datum/NULL construction", typ, input, err)
			}
		}
	}
}
