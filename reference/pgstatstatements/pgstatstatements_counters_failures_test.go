package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCountersFailures(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%v", prepared), func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				pgssCounterSetup(s)
				name := fmt.Sprintf("pgss_counter_failure_%v", prepared)
				target := "pgss_part." + name
				source := target + "_source"
				s.exec("CREATE TABLE " + target + "(k integer PRIMARY KEY,v text)")
				s.exec("CREATE TABLE " + source + "(k integer)")
				s.run(fixture.Exec("INSERT INTO "+target+" VALUES(99,'sentinel')", 1))
				s.run(fixture.Exec("INSERT INTO "+source+" VALUES(1),(2)", 2))
				work := func(step fixture.Step) {
					step.Prepared = prepared
					s.work(step)
				}
				read := "SELECT k/$1::integer AS v FROM " + source + " ORDER BY k"
				work(fixture.Select(read, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, int64(1)))
				before := pgssCounterSnapshot(s, read)
				failure := fixture.ImplicitError(read, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, int64(0))
				failure.SelectRows, failure.ErrorStage = true, "execute"
				work(failure)
				pgssCounterCheck(s, read, 1, 2, pgssCounterWant{read: 1, pgShared: true, serial: true})
				pgssResetEqual(s.t, pgssCounterSnapshot(s, read), before)
				late := "INSERT INTO " + target + " SELECT k,(100/(k-$1::integer))::text FROM " + source + " ORDER BY k RETURNING k,v"
				latePattern := "INSERT INTO " + target + " SELECT%FROM " + source + " ORDER BY k RETURNING k,v"
				s.counts(latePattern, 0, 0, 0)
				failure = fixture.ImplicitError(late, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, int64(2))
				failure.SelectRows, failure.ErrorStage = true, "execute"
				work(failure)
				s.counts(latePattern, 0, 0, 0)
				duplicate := "INSERT INTO " + target + " VALUES($1::integer,$2::text),($3::integer,$4::text) RETURNING k,v"
				d := fixture.UniqueReferenceDuplicate(name, name+"_pkey", "Key (k)=(99) already exists.")
				d.Schema = "pgss_part"
				failure = fixture.ErrorDiagnostic(duplicate, d, int64(7), "rejected-prefix", int64(99), "duplicate")
				failure.SelectRows, failure.ErrorStage = true, "execute"
				work(failure)
				s.counts(duplicate, 0, 0, 0)
				missing := "SELECT k FROM pgss_counter_missing_relation"
				failure = fixture.ImplicitError(missing, fixture.Diagnostic{Code: "42P01", Message: `relation "pgss_counter_missing_relation" does not exist`, Position: fixture.Position(15)})
				failure.SelectRows = true
				work(failure)
				s.counts(missing, 0, 0, 0)
				s.both(fixture.Select("SELECT k,v FROM "+target, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}}))
				// Genuine queued relation admission, not a pre-canceled request.
				insert := "INSERT INTO " + target + " VALUES($1::integer,$2::text)"
				held, err := s.worker.PrepareContext(s.p.Ctx, insert)
				if err != nil {
					s.t.Fatal(err)
				}
				defer held.Close()
				s.exec("BEGIN")
				defer s.observer.ExecContext(s.p.Ctx, "ROLLBACK")
				s.exec("LOCK TABLE " + target + " IN ACCESS EXCLUSIVE MODE")
				// The fixture asserts complete 57014/user-request diagnostics.
				fixture.RelationLockActiveCancel(s.t, s.p, s.b, s.worker, insert, func(ctx context.Context) fixture.RelationOutcome {
					var result fixture.RelationOutcome
					if prepared {
						result.Result, result.Err = held.ExecContext(ctx, int64(8), "cancel")
					} else {
						result.Result, result.Err = s.worker.ExecContext(ctx, insert, int64(8), "cancel")
					}
					return result
				})
				s.exec("ROLLBACK")
				s.counts(insert, 0, 0, 0)
				pgssResetEqual(s.t, pgssCounterSnapshot(s, read), before)
				s.both(fixture.Select("SELECT k,v FROM "+target, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}}))
			})
		})
	}
	t.Run("successful_child_failed_outer", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssCounterSetup(s)
			s.exec("CREATE TABLE pgss_part.pgss_counter_failed_outer_data(k integer,v text)")
			s.exec("CREATE FUNCTION pgss_part.pgss_counter_failed_outer() RETURNS integer LANGUAGE plpgsql AS $$BEGIN INSERT INTO pgss_part.pgss_counter_failed_outer_data VALUES(7,'child'); RAISE EXCEPTION 'counter parent failed'; END$$")
			s.work(fixture.Exec("SET pg_stat_statements.track='all'", 0))
			q := "SELECT pgss_part.pgss_counter_failed_outer() AS v"
			failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "P0001", Message: "counter parent failed", Context: "PL/pgSQL function pgss_part.pgss_counter_failed_outer() line 1 at RAISE"})
			failure.SelectRows = true
			s.work(fixture.Prepared(failure))
			s.counts(q, 0, 0, 0)
			pgssCounterCheckTop(s, "INSERT INTO pgss_part.pgss_counter_failed_outer_data VALUES%", false, 1, 1, pgssCounterWant{dirty: 1, records: 1, bytes: 72, pgWAL: true, serial: true})
			s.both(fixture.Select("SELECT k,v FROM pgss_part.pgss_counter_failed_outer_data", []string{"k", "v"}, []string{"INT4", "TEXT"}, nil))
		})
	})
}
