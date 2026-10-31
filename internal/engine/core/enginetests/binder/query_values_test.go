package binder_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryFromValuesCommonSchema(t *testing.T) {
	for _, tc := range []struct {
		query  string
		id     sqltype.ID
		typmod int32
	}{
		{"VALUES(NULL),(NULL),(1)", sqltype.Int4, -1},
		{"VALUES(NULL),(NULL)", sqltype.Text, -1},
		{"VALUES('1'),(2)", sqltype.Int4, -1},
		{"VALUES('a'::char(2)),('b'::char(2))", sqltype.BPChar, 6},
		{"VALUES(NULL),('a'::char(2))", sqltype.BPChar, -1},
		{"VALUES('a'::varchar(2)),('b'::varchar(3))", sqltype.VarChar, -1},
	} {
		t.Run(tc.query, func(t *testing.T) {
			b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
			query, err := b.BindSelect(enginetest.Statement(t, tc.query).(*parser.Select), false)
			if err != nil {
				t.Fatal(err)
			}
			column := query.Root.Schema()[0]
			if column.Name != "column1" || column.Usage.Type.ID() != tc.id || column.Usage.Typmod != tc.typmod || column.NullabilityKnown {
				t.Fatal(column)
			}
			output := query.Root.(*plan.ProjectNode).Expressions[0].(plan.ColumnExpr)
			if output.Origin != (ids.ObjectRef{}) || len(b.Query.Sources) != 0 || len(b.Target.Uses) != 0 {
				t.Fatal("synthetic source acquired physical identity", output, b.Query.Sources)
			}
			for _, dependency := range b.Dependencies {
				if dependency.Ref.Address.ClassID == ids.RelationClassID {
					t.Fatal("synthetic source acquired relation dependency", dependency)
				}
			}
		})
	}
}

func TestQueryFromValuesCellPrecedenceAndStarts(t *testing.T) {
	for _, tc := range []struct{ query, code, message, position string }{
		{"VALUES(1),(2,DEFAULT)", "42601", "DEFAULT is not allowed in this context", "14"},
		{"VALUES(1),(missing,2)", "42703", `column "missing" does not exist`, "12"},
		{"VALUES(1),(2+3,4)", "42601", "VALUES lists must all be the same length", "12"},
		{"VALUES(1),(2::integer,4)", "42601", "VALUES lists must all be the same length", "12"},
		{"VALUES(1),(TRUE)", "42804", "VALUES types integer and boolean cannot be matched", "12"},
	} {
		b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
		_, err := b.BindSelect(enginetest.Statement(t, tc.query).(*parser.Select), false)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal(tc.query, err, diagnostic, want)
		}
		if b.Query.Namespace != nil || b.Query.NonLateral {
			t.Fatal("VALUES leaked cell namespace")
		}
	}
}
