package core_test

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"testing/synctest"
)

func databaseResetParameterExecute(e *core.Engine, s *session.Session, statement parser.Statement, description *core.Description, input *string) (sqltype.Value, error) {
	op, err := s.Acquire(context.Background())
	if err != nil {
		return sqltype.Value{}, err
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return sqltype.Value{}, err
	}
	scope.Expected = description
	result, err := scope.Execute(statement, 1, func(ctx context.Context, parameters analyze.ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
		if input == nil {
			frame[0] = sqltype.Null(parameters.Types[0].Type)
			return nil
		}
		value, err := parameters.Types[0].Type.InputWithEnvironment(ctx, *input, -1, environment)
		frame[0] = value
		return err
	})
	err = scope.Finish(err)
	if result.Rows != nil {
		defer result.Rows.Close()
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if result.Rows == nil {
		return sqltype.Value{}, fmt.Errorf("parameter reset returned no cursor")
	}
	var row [1]sqltype.Value
	present, err := result.Rows.Next(op.Context(), row[:])
	if err != nil {
		return sqltype.Value{}, err
	}
	if !present || row[0].Type() == nil || row[0].Type().ID() != sqltype.Void {
		return sqltype.Value{}, fmt.Errorf("parameter reset did not return typed VOID")
	}
	value := row[0]
	present, err = result.Rows.Next(op.Context(), row[:])
	if err != nil {
		return sqltype.Value{}, err
	}
	if present {
		return sqltype.Value{}, fmt.Errorf("parameter reset returned multiple rows")
	}
	return value, nil
}

// Public fixtures own parameter SQL/diagnostics. This composed witness adds
// genuine function-collector effects without an adjacent function-statistics view.
func TestStatsResetDatabaseParameterPrivileges(t *testing.T) {
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				x := databaseResetFixtureOf(t)
				statsFunctionExec(t, x.s, "SET track_functions=pl", "SELECT database_reset_f()", "SELECT database_reset_f()")
				statsFunctionRetire(t, x.s)
				x.s = enginetest.StartSession(t, x.e, nil)
				if got := x.e.Statistics.Read(x.f); got.Calls != 2 {
					t.Fatal("genuine PL baseline absent", got)
				}
				statsFunctionExec(t, x.s, "SET plan_cache_mode="+mode, "CREATE ROLE reset_parameter_function_reader")
				statement, description := enginetest.PreparedDescription(t, x.e, x.s, "SELECT pg_stat_reset_single_function_counters($1::oid)")
				defer x.e.RetirePreparedDescription(&description)
				oid := fmt.Sprint(x.f.Object.Address.ObjectID)
				for _, granted := range []bool{false, true, false} {
					statsFunctionExec(t, x.s, "RESET ROLE")
					action := "REVOKE EXECUTE ON FUNCTION pg_stat_reset_single_function_counters(oid) FROM reset_parameter_function_reader"
					if granted {
						action = "GRANT EXECUTE ON FUNCTION pg_stat_reset_single_function_counters(oid) TO reset_parameter_function_reader"
					}
					statsFunctionExec(t, x.s, action, "SET ROLE reset_parameter_function_reader")
					for _, input := range []*string{nil, &oid} {
						before := x.e.Statistics.Read(x.f)
						stamp := x.e.Statistics.Read(x.db).Database.ResetAt
						if before.Calls != 2 {
							t.Fatal("parameter admission lacks genuine positive collector baseline", before)
						}
						value, err := databaseResetParameterExecute(x.e, x.s, statement, &description, input)
						denied := !granted && (input != nil || mode == "force_generic_plan")
						if denied {
							enginetest.Diagnostic(t, err, sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for function pg_stat_reset_single_function_counters"})
						} else if err != nil || value.IsNull() != (input == nil) {
							t.Fatal("typed parameter reset result", value, err)
						}
						if denied || input == nil {
							if x.e.Statistics.Read(x.f) != before || !x.e.Statistics.Read(x.db).Database.ResetAt.Equal(stamp) {
								t.Fatal("noninvoking parameter changed genuine function counters/timestamp")
							}
						} else {
							if got := x.e.Statistics.Read(x.f); got != (statistics.Fact{ResetAt: before.ResetAt, Generation: before.Generation}) || x.e.Statistics.Read(x.db).Database.ResetAt.IsZero() {
								t.Fatal("granted parameter did not reset genuine function history", got)
							}
						}
					}
					if granted {
						producer := enginetest.StartSession(t, x.e, nil)
						statsFunctionExec(t, producer, "SET track_functions=pl", "SELECT database_reset_f()", "SELECT database_reset_f()")
						statsFunctionRetire(t, producer)
						if got := x.e.Statistics.Read(x.f); got.Calls != 2 {
							t.Fatal("post-reset genuine PL baseline absent", got)
						}
					}
				}
				statsFunctionExec(t, x.s, "RESET ROLE")
			})
		})
	}
}
