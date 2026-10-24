package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLRaiseFormatting(t *testing.T) {
	for _, tc := range []struct{ name, body, message string }{
		{"substitution", "RAISE NOTICE 'n=% null=% pct=%%',7,NULL;", "n=7 null=<NULL> pct=%"},
		{"escaped", "RAISE NOTICE '%%';", "%"},
		{"odd", "RAISE NOTICE '%%%',7;", "%7"},
		{"not_printf", "RAISE NOTICE '%s',7;", "7s"},
		{"empty", "RAISE NOTICE '';", ""},
		{"typed", "RAISE NOTICE '% / % / %',true,DATE '2024-01-02',ARRAY[1,2];", "t / 2024-01-02 / {1,2}"},
		{"nested", "RAISE NOTICE '% / %',coalesce(NULL,7),(SELECT 8);", "7 / 8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
				var records []fixture.Diagnostic
				_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &records)
				for _, prepared := range []bool{false, true} {
					records = nil
					s := fixture.Exec(raiseAssertDO(tc.body), 0)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, rec, s)
					raiseAssertNotices(t, records, []fixture.Diagnostic{raiseAssertNotice("NOTICE", "00000", tc.message, "inline_code_block")})
				}
			})
		})
	}
	for _, tc := range []struct{ name, body, message string }{
		{"few", "RAISE '%';", "too few parameters specified for RAISE"},
		{"many", "RAISE 'x',1;", "too many parameters specified for RAISE"},
		{"unreachable", "IF false THEN RAISE '%'; END IF;", "too few parameters specified for RAISE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn) {
				want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
				want.Context = "compilation of PL/pgSQL function \"inline_code_block\" near line 1"
				plpgsqlError(t, p, c, raiseAssertDO(tc.body), false, want)
				q := "CREATE FUNCTION raise_bad() RETURNS integer LANGUAGE plpgsql AS $$BEGIN " + tc.body + " RETURN 7; END$$"
				want.Context = "compilation of PL/pgSQL function \"raise_bad\" near line 1"
				plpgsqlError(t, p, c, q, false, want)
				var count int
				if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_proc WHERE proname='raise_bad'").Scan(&count); err != nil || count != 0 {
					t.Fatalf("failed function published: count=%d err=%v", count, err)
				}
			})
		})
	}
	t.Run("ordered_once_and_output_environment", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
			var records []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &records)
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE SEQUENCE raise_format_seq", 0))
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("SET DateStyle TO 'SQL, DMY'", 0))
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("ALTER SEQUENCE raise_format_seq RESTART WITH 1", 0))
				records = nil
				s := fixture.Exec(raiseAssertDO("RAISE NOTICE '% % %',nextval('raise_format_seq'),nextval('raise_format_seq'),DATE '2024-01-02';"), 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, rec, s)
				raiseAssertNotices(t, records, []fixture.Diagnostic{raiseAssertNotice("NOTICE", "00000", "1 2 02/01/2024", "inline_code_block")})
				fixture.RunStep(t, p.Ctx, c, rec, fixture.Select("SELECT last_value AS v FROM raise_format_seq", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}}))
			}
		})
	})
}

func TestReferencePLpgSQLRaiseConditions(t *testing.T) {
	for _, tc := range []struct{ name, body, code, message string }{
		{"condition", "RAISE division_by_zero;", "22012", "division_by_zero"},
		{"sqlstate", "RAISE SQLSTATE 'ZX987';", "ZX987", "ZX987"},
		{"default", "RAISE 'boom';", "P0001", "boom"},
		{"first_alias", "RAISE modifying_sql_data_not_permitted;", "2F002", "modifying_sql_data_not_permitted"},
		{"dynamic_condition", "RAISE USING ERRCODE='division_by_zero';", "22012", "division_by_zero"},
		{"dynamic_invalid", "RAISE USING ERRCODE='no_such_condition';", "42704", "unrecognized exception condition \"no_such_condition\""},
		{"dynamic_empty", "RAISE USING ERRCODE='';", "42704", "unrecognized exception condition \"\""},
		{"direct_zero", "RAISE SQLSTATE '00000';", "P0001", "00000"},
		{"using_zero", "RAISE USING ERRCODE='00000';", "P0001", "00000"},
		{"zero_then_code", "RAISE SQLSTATE '00000' USING ERRCODE='ZX987';", "ZX987", "ZX987"},
		{"using_zero_then_code", "RAISE USING ERRCODE='00000',ERRCODE='ZX987';", "ZX987", "ZX987"},
		{"code_then_zero", "RAISE USING ERRCODE='ZX987',ERRCODE='00000';", "42601", "RAISE option already specified: ERRCODE"},
		{"repeated_zero", "RAISE USING ERRCODE='00000',ERRCODE='00000';", "P0001", "00000"},
		{"zero_then_null", "RAISE USING ERRCODE='00000',ERRCODE=NULL;", "22004", "RAISE statement option cannot be null"},
		{"zero_then_invalid", "RAISE USING ERRCODE='00000',ERRCODE='bad';", "42704", "unrecognized exception condition \"bad\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn) {
				raiseAssertError(t, p, c, tc.body, tc.code, tc.message, "RAISE")
			})
		})
	}
	for _, tc := range []struct{ name, body, code, message string }{
		{"unknown", "RAISE no_such_condition;", "42704", "unrecognized exception condition \"no_such_condition\""},
		{"others", "RAISE others;", "42704", "unrecognized exception condition \"others\""},
		{"short", "RAISE SQLSTATE '1234';", "42601", "invalid SQLSTATE code at or near \"'1234'\""},
		{"lowercase", "RAISE SQLSTATE 'zx987';", "42601", "invalid SQLSTATE code at or near \"'zx987'\""},
		{"punctuation", "RAISE SQLSTATE 'ZX98!';", "42601", "invalid SQLSTATE code at or near \"'ZX98!'\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn) {
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Context = "compilation of PL/pgSQL function \"inline_code_block\" near line 1"
				if tc.code == "42601" {
					want.Position, want.Context = fixture.Position(27), ""
				}
				plpgsqlError(t, p, c, raiseAssertDO(tc.body), false, want)
			})
		})
	}
	t.Run("reached_only_and_rethrow", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				raiseAssertDO("IF false THEN RAISE USING ERRCODE='bad'; END IF;"),
				"CREATE FUNCTION raise_rethrow() RETURNS integer LANGUAGE plpgsql AS $$BEGIN BEGIN RAISE EXCEPTION USING ERRCODE='ZX987',MESSAGE='original',DETAIL='d',HINT='h'; EXCEPTION WHEN SQLSTATE 'ZX987' THEN BEGIN RAISE; EXCEPTION WHEN SQLSTATE 'ZX987' THEN RAISE; END; END; END$$")
			want := plpgsqlRuntimeDiagnostic("ZX987", "original", "raise_rethrow()", "RAISE")
			want.Detail, want.Hint = "d", "h"
			plpgsqlError(t, p, c, "SELECT raise_rethrow() AS v", true, want)
		})
	})
}
