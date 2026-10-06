package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestExpressionArithmeticCastPreflight(t *testing.T) {
	for _, tc := range []struct {
		query string
		unary bool
		want  int64
		code  string
	}{
		{"SELECT CAST($1::text AS int4)/0 FROM t", false, 0, "22012"},
		{"SELECT 1+CAST($1::text AS int4) FROM t", false, 3, ""},
		{"SELECT -CAST($1::text AS int4) FROM t", true, -2, ""},
	} {
		original := preflightCast(t, tc.query).(arithmeticExpression)
		conversion := original.left
		if !tc.unary && tc.code == "" {
			conversion = original.right
		}
		cast := conversion.(coercionExpression)
		source := cast.child.ResultUsage().Type
		value, err := source.Input(context.Background(), "2", -1)
		if err != nil {
			t.Fatal(err)
		}
		evaluation := EvalContext{Context: context.Background(), Parameters: []sqltype.Value{value}}
		folded, changed, err := preflightCoercions(&evaluation, original)
		if err != nil || !changed {
			t.Fatal("descendant preflight", tc.query, folded, changed, err)
		}
		private, ok := folded.(arithmeticExpression)
		if !ok || private.expressionInfo != original.expressionInfo || !private.Properties().ParameterDependent {
			t.Fatal("arithmetic root folded or metadata changed", folded)
		}
		child := private.left
		if !tc.unary && tc.code == "" {
			child = private.right
		}
		if _, ok := child.(literalExpression); !ok {
			t.Fatal("cast child not materialized", child)
		}
		if _, ok := conversion.(coercionExpression); !ok {
			t.Fatal("original conversion mutated", conversion)
		}
		// The transformed root still delegates to the actual arithmetic provider.
		// Division by zero cannot have been evaluated during the earlier walk.
		evaluation.Parameters[0] = sqltype.Null(source)
		got, err := private.Eval(&evaluation)
		if tc.code != "" {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != "division by zero" {
				t.Fatal(err)
			}
		} else {
			n, valueErr := got.Integer()
			if err != nil || valueErr != nil || n != tc.want {
				t.Fatal(got, n, err, valueErr)
			}
		}
		bad, err := source.Input(context.Background(), "oops", -1)
		if err != nil {
			t.Fatal(err)
		}
		evaluation.Parameters[0] = bad
		_, _, err = preflightCoercions(&evaluation, original)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" || diagnostic.Position != "" || diagnostic.Context != "" {
			t.Fatal("original reused a folded frame", err)
		}
	}
}

func TestExpressionArithmeticCastOrigins(t *testing.T) {
	for _, filter := range []string{"id=2", "FALSE"} {
		_, query, source := queryCoreFixture(t, "SELECT v::bigint+1,CAST(v+1 AS bigint),(v+1)::bigint,v::int4 FROM t WHERE "+filter)
		schema := query.Root.Schema()
		for i, name := range []string{"?column?", "int8", "int8"} {
			column := schema[i]
			if column.Name != name || column.Usage.Type.ID() != sqltype.Int8 || column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 {
				t.Fatal(column)
			}
		}
		if schema[3].Name != "v" || schema[3].OriginTable == 0 || schema[3].OriginAttribute == 0 {
			t.Fatal("identity column origin lost", schema[3])
		}
		rows, count, err := finalizeQuery(context.Background(), query, source, nil)
		if err != nil {
			t.Fatal(err)
		}
		rows.Close()
		want := 1
		if filter == "FALSE" {
			want = 0
		}
		if count != want {
			t.Fatal(filter, count, want)
		}
	}
}
