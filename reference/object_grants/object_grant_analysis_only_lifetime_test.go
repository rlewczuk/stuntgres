package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantAnalysisOnlyLifetime(t *testing.T) {
	t.Run("dependencies", func(t *testing.T) {
		objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			run(fixture.Exec("CREATE TABLE admission_cte_base(n INT)", 0))
			run(fixture.Exec("INSERT INTO admission_cte_base VALUES(-7)", 1))
			run(fixture.Exec("CREATE VIEW admission_cte_view AS WITH unused AS (SELECT n FROM admission_cte_base) SELECT 1 AS v", 0))
			run(fixture.Exec("CREATE TABLE admission_window_base(id INT,n INT)", 0))
			run(fixture.Exec("INSERT INTO admission_window_base VALUES(1,-7),(2,-3)", 2))
			run(fixture.Exec("CREATE VIEW admission_window_view AS SELECT id FROM admission_window_base WINDOW unused AS (ORDER BY n)", 0))
			for _, tc := range []struct{ query, message, detail string }{
				{"DROP TABLE admission_cte_base RESTRICT", "cannot drop table admission_cte_base because other objects depend on it", "view admission_cte_view depends on table admission_cte_base"},
				{"DROP TABLE admission_window_base RESTRICT", "cannot drop table admission_window_base because other objects depend on it", "view admission_window_view depends on table admission_window_base"},
				{"ALTER TABLE admission_window_base DROP COLUMN n RESTRICT", "cannot drop column n of table admission_window_base because other objects depend on it", "view admission_window_view depends on column n of table admission_window_base"},
			} {
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "2BP01", Message: tc.message, Detail: tc.detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT v FROM admission_cte_view", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(fixture.Select("SELECT id FROM admission_window_view ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				run(fixture.Select("SELECT n FROM admission_cte_base", []string{"n"}, []string{"INT4"}, [][]any{{int64(-7)}}))
				run(fixture.Select("SELECT id,n FROM admission_window_base ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(-7)}, {int64(2), int64(-3)}}))
			}
		})
	})
	t.Run("namespace", func(t *testing.T) {
		objectGrantScenario(t, []string{"admission_path_a", "admission_path_b"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			for _, q := range []string{
				"CREATE SCHEMA admission_path_a", "CREATE SCHEMA admission_path_b",
				"CREATE TABLE admission_path_a.source(id INT)", "CREATE TABLE admission_path_b.source(other INT)",
				"SET search_path TO admission_path_a,public",
			} {
				run(fixture.Exec(q, 0))
			}
			q := "WITH unused AS (SELECT id FROM source) SELECT 1 AS v"
			s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			run(s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Exec("SET search_path TO admission_path_b,public", 0))
			denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42703", Message: `column "id" does not exist`, Position: fixture.Position(strings.Index(q, "id") + 1)})
			denied.SelectRows, denied.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
			if prepared {
				denied.ErrorStage = "prepare"
			}
			run(denied)
			run(fixture.Select("WITH unused AS (SELECT id FROM admission_path_a.source) SELECT 1 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Exec("SET search_path TO public", 0))
			run(fixture.Exec("CREATE TABLE admission_window_source(id INT,n INT)", 0))
			run(fixture.Exec("INSERT INTO admission_window_source VALUES(1,-7),(2,-3)", 2))
			q = "SELECT id FROM admission_window_source WINDOW unused AS (ORDER BY n) ORDER BY id"
			s = fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
			windowStmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer windowStmt.Close()
			windowHeld := fixture.HeldStatement{Executor: c, Statement: windowStmt, SQL: q}
			run(s)
			fixture.RunStep(t, p.Ctx, windowHeld, b.Recorder, s)
			run(fixture.Exec("ALTER TABLE admission_window_source RENAME COLUMN n TO renamed_n", 0))
			denied = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42703", Message: `column "n" does not exist`, Position: fixture.Position(strings.Index(q, "n) ORDER") + 1)})
			denied.SelectRows, denied.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, windowHeld, b.Recorder, denied)
			if prepared {
				denied.ErrorStage = "prepare"
			}
			run(denied)
			run(fixture.Exec("ALTER TABLE admission_window_source RENAME COLUMN renamed_n TO n", 0))
			run(s)
			fixture.RunStep(t, p.Ctx, windowHeld, b.Recorder, s)
		})
	})
	t.Run("authority", func(t *testing.T) {
		objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.abs(integer)"})
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
			run(fixture.Exec("CREATE TABLE admission_input(id INT,n INT)", 0))
			run(fixture.Exec("INSERT INTO admission_input VALUES(1,-7),(2,-3)", 2))
			run(fixture.Exec("GRANT SELECT(id) ON admission_input TO og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Select("WITH unused AS (SELECT n FROM admission_input) SELECT 1 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				for _, tail := range []string{"", " WHERE FALSE", " LIMIT 0"} {
					s := fixture.ImplicitError("WITH c AS (SELECT n FROM admission_input"+tail+") SELECT n FROM c", fixture.Diagnostic{Code: "42501", Message: "permission denied for table admission_input"})
					s.SelectRows, s.ErrorStage = true, "execute"
					run(s)
				}
			})
			run(fixture.Exec("GRANT SELECT ON admission_input TO og_object_reader", 0))
			run(fixture.Exec("CREATE VIEW admission_owner_view AS SELECT id,pg_catalog.abs(n) AS v FROM admission_input", 0))
			run(fixture.Exec("GRANT SELECT ON admission_owner_view TO og_object_reader", 0))
			step := fixture.Select("SELECT id,v FROM admission_owner_view ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(3)}})
			objectGrantProviderLifecycle(t, p, b, c, run, "pg_catalog.abs(integer)", "function", "abs", []objectGrantProviderCase{{step, true, true}}, nil)
		})
	})
	t.Run("replacement", func(t *testing.T) {
		objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			run(fixture.Exec("CREATE TABLE admission_replay(id INT,n INT)", 0))
			run(fixture.Exec("INSERT INTO admission_replay VALUES(1,-7),(2,-3)", 2))
			run(fixture.Exec("CREATE VIEW admission_replay_view AS WITH unused AS (SELECT pg_catalog.abs(n) FROM admission_replay) SELECT id FROM admission_replay WINDOW unused AS (ORDER BY pg_catalog.abs(n))", 0))
			step := fixture.Select("SELECT id FROM admission_replay_view ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
			run(step)
			stmt, err := c.PrepareContext(p.Ctx, step.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: step.Query}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			run(step)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		})
	})
}
