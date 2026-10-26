package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSQLExecutionBinding(t *testing.T) {
	t.Run("value_not_identifier", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE sx_sentinel(i integer)", "CREATE TABLE sx_names(sx_names text)",
				"CREATE FUNCTION sx_names() RETURNS integer LANGUAGE SQL AS 'SELECT 9'",
				"CREATE FUNCTION sx_bind(payload text) RETURNS integer LANGUAGE plpgsql AS $$DECLARE sx_names text:='not_a_relation'; BEGIN INSERT INTO sx_names(sx_names) VALUES(payload); PERFORM sx_names(); RETURN sx_names(); END$$")
			payload := "x'; DROP TABLE sx_sentinel; --"
			for _, prepared := range []bool{false, true} {
				s := sqlExecutionStep(b, "SELECT sx_bind($1::text) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}, payload)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				sqlExecutionValue(t, p, b, c, "SELECT sx_names||'' AS v FROM sx_names", "TEXT", payload)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sx_sentinel VALUES(1)", 1))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM sx_names", 1))
			}
			sqlExecutionCount(t, p, b, c, "sx_sentinel", 2)
		})
	})
	t.Run("ambiguity_and_qualification", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE sx_collision(x integer)",
				"CREATE FUNCTION sx_ambiguous() RETURNS void LANGUAGE plpgsql AS $$DECLARE x integer:=4; BEGIN PERFORM x FROM sx_collision; END$$",
				"CREATE FUNCTION sx_qualified(x integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE a integer; b integer; BEGIN SELECT sx_collision.x,sx_qualified.x INTO a,b FROM sx_collision; <<blk>> DECLARE x integer:=4; BEGIN PERFORM blk.x FROM sx_collision; END; RETURN a||':'||b; END$$")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sx_collision VALUES(9)", 1))
			want := plpgsqlRuntimeDiagnostic("42702", `column reference "x" is ambiguous`, "sx_ambiguous()", "PERFORM")
			want.Detail = "It could refer to either a PL/pgSQL variable or a table column."
			want.InternalQuery, want.InternalPosition = "SELECT x FROM sx_collision", "8"
			plpgsqlError(t, p, c, "SELECT sx_ambiguous()", true, want)
			sqlExecutionValue(t, p, b, c, "SELECT sx_qualified(4) AS v", "TEXT", "9:4")
		})
	})
	t.Run("reached_binding_and_full_syntax", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION sx_lazy(take_it boolean) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF take_it THEN PERFORM * FROM sx_missing; END IF; RETURN 7; END$$")
			sqlExecutionValue(t, p, b, c, "SELECT sx_lazy(false) AS v", "INT4", int64(7))
			want := plpgsqlRuntimeDiagnostic("42P01", `relation "sx_missing" does not exist`, "sx_lazy(boolean)", "PERFORM")
			want.InternalQuery, want.InternalPosition = "SELECT * FROM sx_missing", "15"
			plpgsqlError(t, p, c, "SELECT sx_lazy(true)", true, want)
			q := "CREATE FUNCTION sx_bad() RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF false THEN PERFORM 'é', FROM sx_missing; END IF; RETURN 1; END$$"
			d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "FROM"`)
			d.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "FROM sx_missing")]) + 1)
			plpgsqlError(t, p, c, q, false, d)
			sqlExecutionCount(t, p, b, c, "pg_proc WHERE proname='sx_bad'", 0)
		})
	})
}
