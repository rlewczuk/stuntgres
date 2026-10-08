package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAlterTypePreparedSourceMetadata(t *testing.T) {
	for _, tc := range []struct {
		old, next      string
		source, target sqltype.ID
	}{{"INT", "BIGINT", sqltype.Int4, sqltype.Int8}, {"BIGINT", "INT", sqltype.Int8, sqltype.Int4}} {
		t.Run(tc.old+"-"+tc.next, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(c "+tc.old+")")
			op, cleanup, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			ast := statement(t, "INSERT INTO t VALUES($1)")
			held, err := e.Describe(op, ast)
			if err != nil || len(held.Parameters.Types) != 1 || held.Parameters.Types[0].Type.ID() != tc.source {
				t.Fatal(held, err)
			}
			original := held.Parameters.Types[0]
			run(t, e, "ALTER TABLE t ALTER c TYPE "+tc.next)
			fresh, err := e.Describe(op, ast)
			if err != nil || len(fresh.Parameters.Types) != 1 || fresh.Parameters.Types[0].Type.ID() != tc.target {
				t.Fatal(fresh, err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			scope.Expected = &held
			_, executeErr := scope.Execute(ast, 1, func(ctx context.Context, parameters ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
				if parameters.Types[0] != original {
					t.Fatal("held input metadata changed", parameters.Types, original)
				}
				value, err := parameters.Types[0].Type.InputWithEnvironment(ctx, "7", -1, environment)
				frame[0] = value
				return err
			})
			finishErr := scope.Finish(executeErr)
			if executeErr != nil || finishErr != nil || held.Parameters.Types[0] != original {
				t.Fatal(executeErr, finishErr, held)
			}
			if tc.source == sqltype.Int8 {
				narrowing, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				narrowing.Expected = &held
				_, executeErr := narrowing.Execute(ast, 1, func(ctx context.Context, parameters ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
					if parameters.Types[0] != original {
						t.Fatal("narrowing changed held source metadata", parameters.Types)
					}
					value, err := parameters.Types[0].Type.InputWithEnvironment(ctx, "2147483648", -1, environment)
					frame[0] = value
					return err
				})
				finishErr := narrowing.Finish(executeErr)
				state(t, executeErr, "22003")
				state(t, finishErr, "22003")
			}
			result := run(t, e, "SELECT c FROM t")
			rows := tuples(result)
			if result.Rows.Schema()[0].Usage.Type.ID() != tc.target || len(rows) != 1 || rows[0][0] != int64(7) {
				t.Fatal(result, rows)
			}
		})
	}
}
