package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgTrgmThresholdTransactions(t *testing.T) {
	pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := pgTrgmRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
		getSQL := "SELECT show_limit() AS value"
		getStmt, err := c.PrepareContext(p.Ctx, getSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer getStmt.Close()
		getter := fixture.HeldStatement{Executor: c, Statement: getStmt, SQL: getSQL}
		matchSQL := "SELECT 'a' % 'aa' AS match"
		matchStmt, err := c.PrepareContext(p.Ctx, matchSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer matchStmt.Close()
		match := fixture.HeldStatement{Executor: c, Statement: matchStmt, SQL: matchSQL}
		type transition struct {
			query     string
			effective float32
			result    any
		}
		for _, tc := range []struct {
			name  string
			steps []transition
		}{
			{"Rollback", []transition{
				{"BEGIN", 0.25, nil},
				{"SELECT set_limit(0.75::real)", 0.75, float64(0.75)},
				{"ROLLBACK", 0.25, nil},
			}},
			{"Commit", []transition{
				{"BEGIN", 0.25, nil},
				{"SELECT set_limit(0.75::real)", 0.75, float64(0.75)},
				{"COMMIT", 0.75, nil},
			}},
			{"Savepoint", []transition{
				{"BEGIN", 0.25, nil},
				{"SAVEPOINT s", 0.25, nil},
				{"SELECT set_limit(0.75::real)", 0.75, float64(0.75)},
				{"ROLLBACK TO s", 0.25, nil},
				{"COMMIT", 0.25, nil},
			}},
			{"LocalCommit", []transition{
				{"BEGIN", 0.25, nil},
				{"SET LOCAL pg_trgm.similarity_threshold=1", 1, nil},
				{"COMMIT", 0.25, nil},
			}},
			{"LocalRollback", []transition{
				{"BEGIN", 0.25, nil},
				{"SET LOCAL pg_trgm.similarity_threshold=1", 1, nil},
				{"ROLLBACK", 0.25, nil},
			}},
			{"SessionAfterLocalCommit", []transition{
				{"BEGIN", 0.25, nil},
				{"SET LOCAL pg_trgm.similarity_threshold=1", 1, nil},
				{"SELECT set_limit(0.5::real)", 0.5, float64(0.5)},
				{"COMMIT", 0.5, nil},
			}},
			{"SessionAfterLocalRollback", []transition{
				{"BEGIN", 0.25, nil},
				{"SET LOCAL pg_trgm.similarity_threshold=1", 1, nil},
				{"SELECT set_limit(0.5::real)", 0.5, float64(0.5)},
				{"ROLLBACK", 0.25, nil},
			}},
			{"SavepointRestoresLocal", []transition{
				{"BEGIN", 0.25, nil},
				{"SET LOCAL pg_trgm.similarity_threshold=1", 1, nil},
				{"SAVEPOINT s", 1, nil},
				{"SELECT set_limit(0.5::real)", 0.5, float64(0.5)},
				{"ROLLBACK TO s", 1, nil},
				{"COMMIT", 0.25, nil},
			}},
			{"NullPreservesLocal", []transition{
				{"BEGIN", 0.25, nil},
				{"SET LOCAL pg_trgm.similarity_threshold=1", 1, nil},
				{"SELECT set_limit(NULL::real)", 1, nil},
				{"COMMIT", 0.25, nil},
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				run := pgTrgmRun(t, p, b, c)
				check := func(value float32) {
					fixture.RunStep(t, p.Ctx, getter, b.Recorder, pgTrgmScalar(b, getSQL, "value", "FLOAT4", pgTrgmFloat(b.PG, value)))
					fixture.RunStep(t, p.Ctx, match, b.Recorder, pgTrgmScalar(b, matchSQL, "match", "BOOL", value <= 0.25))
				}
				run(fixture.Exec("SET pg_trgm.similarity_threshold='0.25'", 0))
				check(0.25)
				for _, step := range tc.steps {
					if strings.HasPrefix(step.query, "SELECT ") {
						run(pgTrgmScalar(b, step.query, "set_limit", "FLOAT4", step.result))
					} else {
						run(fixture.Exec(step.query, 0))
					}
					check(step.effective)
				}
			})
		}
		t.Run("FailureAbortsTransaction", func(t *testing.T) {
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("SET pg_trgm.similarity_threshold='0.25'", 0))
			run(fixture.Exec("BEGIN", 0))
			run(pgTrgmScalar(b, "SELECT set_limit(0.75::real)", "set_limit", "FLOAT4", float64(0.75)))
			run(pgTrgmFailure("SELECT set_limit(2::real)", fixture.Diagnostic{
				Code: "22023", Message: `2 is outside the valid range for parameter "pg_trgm.similarity_threshold" (0 .. 1)`,
			}))
			run(pgTrgmFailure("SELECT show_limit()", fixture.Diagnostic{
				Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block",
			}))
			run(fixture.Exec("ROLLBACK", 0))
			fixture.RunStep(t, p.Ctx, getter, b.Recorder, pgTrgmScalar(b, getSQL, "value", "FLOAT4", float64(0.25)))
			fixture.RunStep(t, p.Ctx, match, b.Recorder, pgTrgmScalar(b, matchSQL, "match", "BOOL", true))
		})
		pgTrgmNotices(t, notices)
	})
}
