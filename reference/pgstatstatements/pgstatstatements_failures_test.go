package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsFailures(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%v", prepared), func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				suffix := "direct"
				if prepared {
					suffix = "prepared"
				}
				source := "pgss_part.pgss_failure_source_" + suffix
				target := "pgss_part.pgss_failure_target_" + suffix
				s.exec("CREATE TABLE " + source + "(k integer)")
				s.exec("CREATE TABLE " + target + "(k integer,v integer)")
				s.run(fixture.Exec("INSERT INTO "+source+" VALUES(1),(2)", 2))
				s.run(fixture.Exec("INSERT INTO "+target+" VALUES(99,99)", 1))
				work := func(step fixture.Step) {
					if prepared {
						step = fixture.Prepared(step)
					}
					s.work(step)
				}

				selectSQL := "SELECT 10/(k-$1::integer) AS pgss_failure_value FROM " + source + " WHERE k=1"
				selectPattern := "SELECT % AS pgss_failure_value FROM " + source + "%"
				s.counts(selectPattern, 0, 0, 0)
				work(fixture.Select(selectSQL, []string{"pgss_failure_value"}, []string{"INT4"}, [][]any{{int64(10)}}, int64(0)))
				s.counts(selectPattern, 1, 1, 1)
				failure := fixture.ImplicitError(selectSQL, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, int64(1))
				failure.SelectRows, failure.ErrorStage = true, "execute"
				work(failure)
				// Successful planning is disabled here: the failed execution must
				// neither charge a call nor replace the previous successful totals.
				s.counts(selectPattern, 1, 1, 1)

				insertSQL := "INSERT INTO " + target + " SELECT k,100/(k-$1::integer) FROM " + source + " ORDER BY k"
				insertPattern := "INSERT INTO " + target + " SELECT%"
				s.counts(insertPattern, 0, 0, 0)
				failure = fixture.ImplicitError(insertSQL, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, int64(2))
				failure.ErrorStage = "execute"
				work(failure)
				s.counts(insertPattern, 0, 0, 0)
				s.both(fixture.Select("SELECT k,v FROM "+target+" ORDER BY k", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}}))
				// The same parameterized write succeeds afterwards, proving that
				// the failed multi-row statement left neither data nor a fake call.
				work(fixture.Exec(insertSQL, 2, int64(0)))
				s.counts(insertPattern, 1, 1, 2)
				s.both(fixture.Select("SELECT k,v FROM "+target+" ORDER BY k", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(100)}, {int64(2), int64(50)}, {int64(99), int64(99)}}))
			})
		})
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancellation_prepared_%v", prepared), func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				s.exec("CREATE TABLE pgss_part.pgss_canceled_write(k integer)")
				s.run(fixture.Exec("INSERT INTO pgss_part.pgss_canceled_write VALUES(99)", 1))
				q := "INSERT INTO pgss_part.pgss_canceled_write VALUES($1::integer)"
				held, err := s.worker.PrepareContext(s.p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				s.counts(q, 0, 0, 0)
				s.exec("BEGIN")
				defer s.observer.ExecContext(s.p.Ctx, "ROLLBACK")
				s.exec("LOCK TABLE pgss_part.pgss_canceled_write IN ACCESS EXCLUSIVE MODE")
				fixture.RelationLockActiveCancel(t, s.p, s.b, s.worker, q, func(ctx context.Context) fixture.RelationOutcome {
					var outcome fixture.RelationOutcome
					if prepared {
						outcome.Result, outcome.Err = held.ExecContext(ctx, int64(7))
					} else {
						outcome.Result, outcome.Err = s.worker.ExecContext(ctx, q, int64(7))
					}
					return outcome
				})
				s.exec("ROLLBACK")
				s.counts(q, 0, 0, 0)
				s.both(fixture.Select("SELECT k FROM pgss_part.pgss_canceled_write", []string{"k"}, []string{"INT4"}, [][]any{{int64(99)}}))
			})
		})
		t.Run(fmt.Sprintf("successful_child_failed_parent_prepared_%v", prepared), func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				name := "pgss_part.pgss_failed_parent_" + fmt.Sprintf("%v", prepared)
				s.exec("CREATE TABLE " + name + "_data(k integer)")
				s.exec("CREATE FUNCTION " + name + "() RETURNS integer LANGUAGE plpgsql AS $$BEGIN INSERT INTO " + name + "_data VALUES(7); RAISE EXCEPTION 'pgss parent failed'; END$$")
				s.work(fixture.Exec("SET pg_stat_statements.track = 'all'", 0))
				child := "INSERT INTO " + name + "_data VALUES%"
				outer := "SELECT " + name + "() AS pgss_failed_parent_value"
				s.nestedCounts(child, false, 0, 0, 0)
				failure := fixture.ImplicitError(outer, fixture.Diagnostic{Code: "P0001", Message: "pgss parent failed", Context: "PL/pgSQL function " + name + "() line 1 at RAISE"})
				failure.Prepared, failure.SelectRows = prepared, true
				s.work(failure)
				s.counts(outer, 0, 0, 0)
				s.nestedCounts(child, false, 1, 1, 1)
				s.both(fixture.Select("SELECT k FROM "+name+"_data", []string{"k"}, []string{"INT4"}, nil))
			})
		})
	}
}
