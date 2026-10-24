package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSelectIntoTargets(t *testing.T) {
	t.Run("shapes_and_conversion", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE si_shape(i integer,t text)",
				"CREATE FUNCTION si_named(mode integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE r si_shape%ROWTYPE; BEGIN r:=ROW(99,'old'); IF mode=1 THEN SELECT '23'::text AS t,'cast'::text AS i INTO STRICT r; ELSIF mode=2 THEN SELECT 8 INTO r; ELSIF mode=3 THEN SELECT 9,'extra'::text,true INTO r; ELSE SELECT 7,'ignored'::text INTO r WHERE false; END IF; RETURN coalesce(r.i::text,'NULL')||':'||coalesce(r.t,'NULL')||':'||FOUND; END$$",
				"CREATE FUNCTION si_record() RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; s text; BEGIN SELECT 5 AS old_field INTO r; s:=r.old_field::text; SELECT true AS flag,'new'::text AS label INTO STRICT r; s:=s||':'||r.flag||':'||r.label; SELECT 1::bigint AS fresh,'x'::text AS txt INTO r WHERE false; RETURN s||':'||coalesce(r.fresh::text,'NULL')||':'||coalesce(r.txt,'NULL')||':'||FOUND; END$$",
				"CREATE FUNCTION si_old_field() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; BEGIN SELECT 5 AS old_field INTO r; SELECT 1 AS fresh INTO r WHERE false; RETURN r.old_field; END$$",
				"CREATE FUNCTION si_list() RETURNS text LANGUAGE plpgsql AS $$DECLARE a integer:=10; b text:='old'; s text; r si_shape%ROWTYPE; x ALIAS FOR a; BEGIN SELECT '23'::text AS b,'cast'::text AS a INTO STRICT x,b; s:=a||':'||b; SELECT 8 INTO a,b; s:=s||':'||a||':'||coalesce(b,'NULL'); SELECT 1,2 INTO a,a; SELECT 3,'fields'::text INTO r.i,r.t; RETURN s||':'||a||':'||r.i||':'||r.t; END$$",
				"CREATE FUNCTION si_formal(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN <<inner_block>> DECLARE x integer:=99; BEGIN SELECT 7 INTO si_formal.x; SELECT 8 INTO inner_block.x; END; SELECT $1+1 INTO $1; RETURN x; END$$")
			for _, tc := range []struct {
				mode int
				want string
			}{{1, "23:cast:true"}, {2, "8:NULL:true"}, {3, "9:extra:true"}, {4, "NULL:NULL:false"}} {
				s := fixture.Select("SELECT si_named($1) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.mode)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
			plpgsqlValue(t, p, b, c, "SELECT si_record() AS v", "TEXT", "5:true:new:NULL:NULL:false")
			plpgsqlValue(t, p, b, c, "SELECT si_list() AS v", "TEXT", "23:cast:8:NULL:2:3:fields")
			plpgsqlValue(t, p, b, c, "SELECT si_formal(1) AS v", "INT4", int64(8))
			want := plpgsqlRuntimeDiagnostic("42703", `record "r" has no field "old_field"`, "si_old_field()", "RETURN")
			want.Context = "PL/pgSQL expression \"r.old_field\"\n" + want.Context
			plpgsqlError(t, p, c, "SELECT si_old_field() AS v", true, want)
		})
	})
	t.Run("target_authority", func(t *testing.T) {
		for _, tc := range []struct{ name, decl, target, code, message, token string }{
			{"record_list", "r record; x integer;", "r,x", "42601", "record variable cannot be part of multiple-item INTO list", ","},
			{"row_list", "r si_target_row%ROWTYPE; x integer;", "r,x", "42601", "record variable cannot be part of multiple-item INTO list", ","},
			{"later_record", "x integer; r record;", "x,r", "42601", `"r" is not a scalar variable`, "r;"},
			{"later_row", "x integer; r si_target_row%ROWTYPE;", "x,r", "42601", `"r" is not a scalar variable`, "r;"},
			{"unknown", "", "absent", "42601", `"absent" is not a known variable`, "absent"},
			{"constant", "x CONSTANT integer:=1;", "x", "22005", `variable "x" is declared CONSTANT`, "x;"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c, "CREATE TABLE si_target_row(i integer)")
					q := "CREATE FUNCTION si_bad_target() RETURNS void LANGUAGE plpgsql AS $$DECLARE " + tc.decl + " BEGIN RETURN; SELECT 1,2 INTO " + tc.target + "; END$$"
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					start := strings.Index(q, "INTO ") + 5
					i := strings.Index(q[start:], tc.token)
					want.Position = strconv.Itoa(utf8.RuneCountInString(q[:start+i]) + 1)
					// Positions and row-list wording are source-reasoned discovery
					// expectations, not claimed oracle observations.
					plpgsqlError(t, p, c, q, false, want)
					plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='si_bad_target'", "INT8", int64(0))
				})
			})
		}
	})
	t.Run("usage_constraints", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE DOMAIN si_positive AS integer CHECK(VALUE>0)",
				"CREATE FUNCTION si_usage(mode integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer NOT NULL:=9; d si_positive:=1; s varchar(3):='old'; BEGIN IF mode=1 THEN BEGIN SELECT NULL::integer INTO x; EXCEPTION WHEN null_value_not_allowed THEN RETURN x||':'||FOUND; END; ELSIF mode=2 THEN BEGIN SELECT -1 INTO d; EXCEPTION WHEN check_violation THEN RETURN d||':'||FOUND; END; ELSE BEGIN SELECT 'abcdef'::text INTO s; EXCEPTION WHEN string_data_right_truncation THEN RETURN s||':'||FOUND; END; END IF; RETURN 'missed'; END$$")
			for _, tc := range []struct {
				mode int
				want string
			}{{1, "9:true"}, {2, "1:true"}, {3, "old:true"}} {
				s := fixture.Select("SELECT si_usage($1) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.mode)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
		})
	})
	t.Run("sql_boundaries", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				`CREATE FUNCTION si_boundary() RETURNS text LANGUAGE plpgsql AS $fn$DECLARE "into" integer; "strict" text; BEGIN WITH q AS (SELECT 7 AS "into") SELECT (SELECT q."into"), 'into strict'::text /* INTO missing */ INTO "into","strict" FROM q; RETURN "into"||':'||"strict"; END$fn$`,
				"CREATE FUNCTION si_early() RETURNS integer LANGUAGE plpgsql AS $$DECLARE si_destination integer; BEGIN SELECT INTO si_destination 9; RETURN si_destination; END$$",
				"CREATE FUNCTION si_suffix() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN SELECT i FROM (SELECT 11 AS i) q INTO x; RETURN x; END$$")
			plpgsqlValue(t, p, b, c, "SELECT si_boundary() AS v", "TEXT", "7:into strict")
			plpgsqlValue(t, p, b, c, "SELECT si_early() AS v", "INT4", int64(9))
			plpgsqlValue(t, p, b, c, "SELECT si_suffix() AS v", "INT4", int64(11))
			// A procedural destination must not have created a SQL table.
			userOperatorExec(t, p, b, c, "CREATE TABLE si_destination(i integer)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SELECT 4 AS i INTO si_sql_table", 1))
			plpgsqlValue(t, p, b, c, "SELECT i AS v FROM si_sql_table", "INT4", int64(4))
		})
	})
}
