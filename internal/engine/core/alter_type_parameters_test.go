package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

func TestAlterTypePreparedSourceMetadata(t *testing.T) {
	for _, tc := range []struct {
		old, next      string
		source, target sqltype.ID
	}{{"INT", "BIGINT", sqltype.Int4, sqltype.Int8}, {"BIGINT", "INT", sqltype.Int8, sqltype.Int4}} {
		t.Run(tc.old+"-"+tc.next, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE t(c "+tc.old+")")
			op, cleanup, err := enginetest.Operation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			ast := enginetest.Statement(t, "INSERT INTO t VALUES($1)")
			held, err := e.Describe(op, ast)
			if err != nil || len(held.Parameters.Types) != 1 || held.Parameters.Types[0].Type.ID() != tc.source {
				t.Fatal(held, err)
			}
			original := held.Parameters.Types[0]
			enginetest.Run(t, e, "ALTER TABLE t ALTER c TYPE "+tc.next)
			fresh, err := e.Describe(op, ast)
			if err != nil || len(fresh.Parameters.Types) != 1 || fresh.Parameters.Types[0].Type.ID() != tc.target {
				t.Fatal(fresh, err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			scope.Expected = &held
			_, executeErr := scope.Execute(ast, 1, func(ctx context.Context, parameters analyze.ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
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
				_, executeErr := narrowing.Execute(ast, 1, func(ctx context.Context, parameters analyze.ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
					if parameters.Types[0] != original {
						t.Fatal("narrowing changed held source metadata", parameters.Types)
					}
					value, err := parameters.Types[0].Type.InputWithEnvironment(ctx, "2147483648", -1, environment)
					frame[0] = value
					return err
				})
				finishErr := narrowing.Finish(executeErr)
				enginetest.State(t, executeErr, "22003")
				enginetest.State(t, finishErr, "22003")
			}
			result := enginetest.Run(t, e, "SELECT c FROM t")
			rows := enginetest.Tuples(result)
			if result.Rows.Schema()[0].Usage.Type.ID() != tc.target || len(rows) != 1 || rows[0][0] != int64(7) {
				t.Fatal(result, rows)
			}
		})
	}
}
