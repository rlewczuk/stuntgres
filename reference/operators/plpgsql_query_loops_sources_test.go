package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLQueryLoopsSources(t *testing.T) {
	for _, tc := range []struct {
		name, seed, source, want string
		rows                     [][]any
	}{
		{"insert", "", "INSERT INTO ql_source AS dst VALUES(7) RETURNING dst.i AS value", "1|7|true", [][]any{{int64(7)}}},
		{"update", "INSERT INTO ql_source VALUES(7)", "UPDATE ql_source AS dst SET i=i+1 RETURNING dst.i AS value", "1|8|true", [][]any{{int64(8)}}},
		{"delete", "INSERT INTO ql_source VALUES(8)", "DELETE FROM ql_source AS dst RETURNING dst.i AS value", "1|8|true", nil},
		{"empty_returning", "", "UPDATE ql_source SET i=9 RETURNING i AS value", "0|0|false", nil},
		{"duplicates", "", "INSERT INTO ql_source VALUES(2),(1),(2) RETURNING i AS value", "3|5|true", [][]any{{int64(1)}, {int64(2)}, {int64(2)}}},
		{"with_select", "", "WITH q AS (SELECT 7 AS i) SELECT i AS value FROM q", "1|7|true", nil},
		{"with_insert", "", "WITH q AS (SELECT 7 AS i) INSERT INTO ql_source SELECT i FROM q RETURNING i AS value", "1|7|true", [][]any{{int64(7)}}},
		{"with_update", "INSERT INTO ql_source VALUES(7)", "WITH q AS (SELECT 1 AS delta) UPDATE ql_source SET i=i+q.delta FROM q RETURNING i AS value", "1|8|true", [][]any{{int64(8)}}},
		{"with_delete", "INSERT INTO ql_source VALUES(8)", "WITH q AS (SELECT 8 AS key) DELETE FROM ql_source USING q WHERE i=q.key RETURNING i AS value", "1|8|true", nil},
		{"with_merge", "INSERT INTO ql_source VALUES(7)", "WITH q AS (SELECT 7 AS key) MERGE INTO ql_source AS dst USING q ON dst.i=q.key WHEN MATCHED THEN UPDATE SET i=8 RETURNING dst.i AS value", "1|8|true", [][]any{{int64(8)}}},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c, "CREATE TABLE ql_source(i integer)")
					if tc.seed != "" {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(tc.seed, 1))
					}
					userOperatorExec(t, p, b, c, "CREATE FUNCTION ql_source_run() RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; n integer:=0; total integer:=0; BEGIN FOR r IN "+tc.source+" LOOP n:=n+1; total:=total+r.value; END LOOP; IF n=0 THEN IF r.value IS NOT NULL THEN RETURN 'bad empty target'; END IF; END IF; RETURN n||'|'||total||'|'||FOUND; END$$")
					s := fixture.Select("SELECT ql_source_run() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT i FROM ql_source ORDER BY i", []string{"i"}, []string{"INT4"}, tc.rows))
				})
			})
		}
	}
	t.Run("merge_action", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE ql_merge(i integer)",
				"CREATE FUNCTION ql_merge_run() RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; BEGIN FOR r IN MERGE INTO ql_merge AS dst USING (VALUES(7)) AS src(i) ON dst.i=src.i WHEN MATCHED THEN UPDATE SET i=8 WHEN NOT MATCHED THEN INSERT VALUES(src.i) RETURNING merge_action() AS action,dst.i AS value LOOP RETURN r.action||':'||r.value; END LOOP; RETURN 'empty'; END$$")
			for _, tc := range []struct {
				prepared bool
				want     string
				value    int64
			}{{false, "INSERT:7", 7}, {true, "UPDATE:8", 8}} {
				s := fixture.Select("SELECT ql_merge_run() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}})
				s.Prepared = tc.prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT i FROM ql_merge", []string{"i"}, []string{"INT4"}, [][]any{{tc.value}}))
			}
		})
	})
	for _, source := range []string{
		"INSERT INTO ql_complete VALUES(1),(2),(3) RETURNING i",
		"WITH changed AS (INSERT INTO ql_complete VALUES(1),(2),(3) RETURNING i) SELECT i FROM changed",
	} {
		t.Run("eager_completion_"+source, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE ql_complete(i integer)", "CREATE FUNCTION ql_complete_run() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; n integer:=0; BEGIN FOR r IN "+source+" LOOP n:=n+1; EXIT; END LOOP; RETURN n; END$$")
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT ql_complete_run() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT i FROM ql_complete ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ql_complete", 3))
				}
			})
		})
	}
	t.Run("late_returning_error_precedes_body", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			source := "INSERT INTO ql_eager SELECT g FROM generate_series(1,100) AS g RETURNING 100/(100-i) AS value"
			userOperatorExec(t, p, b, c, "CREATE TABLE ql_eager(i integer)", "CREATE SEQUENCE ql_body_called",
				"CREATE FUNCTION ql_eager_run() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; token bigint; BEGIN FOR r IN "+source+" LOOP token:=nextval('ql_body_called'); EXIT; END LOOP; RETURN 1; END$$")
			want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "ql_eager_run()", "FOR over SELECT rows")
			plpgsqlError(t, p, c, "SELECT ql_eager_run() AS v", true, want)
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM ql_eager", "INT8", int64(0))
			plpgsqlValue(t, p, b, c, "SELECT is_called AS v FROM ql_body_called", "BOOL", false)
		})
	})
	for _, source := range []string{
		"INSERT INTO ql_no_rows VALUES(9)",
		"UPDATE ql_no_rows SET i=9",
		"DELETE FROM ql_no_rows",
		"MERGE INTO ql_no_rows AS dst USING (VALUES(7)) AS src(i) ON dst.i=src.i WHEN MATCHED THEN UPDATE SET i=9 WHEN NOT MATCHED THEN INSERT VALUES(src.i)",
	} {
		command := strings.Fields(source)[0]
		t.Run("no_returning_"+command, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE ql_no_rows(i integer)")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ql_no_rows VALUES(7)", 1))
				userOperatorExec(t, p, b, c, "CREATE FUNCTION ql_no_returning() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; BEGIN FOR r IN "+source+" LOOP END LOOP; RETURN 1; END$$")
				want := plpgsqlRuntimeDiagnostic("42P11", "cannot open "+command+" query as cursor", "ql_no_returning()", "FOR over SELECT rows")
				plpgsqlError(t, p, c, "SELECT ql_no_returning() AS v", true, want)
				plpgsqlValue(t, p, b, c, "SELECT i AS v FROM ql_no_rows", "INT4", int64(7))
			})
		})
	}
}
