package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLRaiseOptions(t *testing.T) {
	t.Run("duplicate_expression_effects", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE SEQUENCE raise_duplicate_seq")
			for _, prepared := range []bool{false, true} {
				userOperatorExec(t, p, b, c, "ALTER SEQUENCE raise_duplicate_seq RESTART WITH 1")
				q := raiseAssertDO("RAISE USING DETAIL=nextval('raise_duplicate_seq'),DETAIL=nextval('raise_duplicate_seq');")
				s := fixture.ErrorDiagnostic(q, plpgsqlRuntimeDiagnostic("42601", "RAISE option already specified: DETAIL", "inline_code_block", "RAISE"))
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT last_value AS v FROM raise_duplicate_seq", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}}))
			}
		})
	})
	t.Run("complete_fields", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn) {
			q := "DO $$BEGIN\n  RAISE EXCEPTION USING ERRCODE='ZX987',MESSAGE='boom',DETAIL='detail',HINT='hint',COLUMN='col',CONSTRAINT='con',DATATYPE='dtype',TABLE='tab',SCHEMA='sch';\nEND$$"
			want := fixture.ControlsReferenceDiagnostic("ZX987", "boom")
			want.Detail, want.Hint = "detail", "hint"
			want.Column, want.Constraint, want.DataType, want.Table, want.Schema = "col", "con", "dtype", "tab", "sch"
			want.Context = "PL/pgSQL function inline_code_block line 2 at RAISE"
			plpgsqlError(t, p, c, q, false, want)
			q = "DO $$BEGIN\n  RAISE EXCEPTION USING SCHEMA:='sch',TABLE:='tab',DATATYPE:='dtype',CONSTRAINT:='con',COLUMN:='col',HINT:='hint',DETAIL:='detail',MESSAGE:='boom',ERRCODE:='ZX987';\nEND$$"
			plpgsqlError(t, p, c, q, false, want)
		})
	})
	for _, tc := range []struct{ name, body, code, message, detail string }{
		{"detail_only", "RAISE USING DETAIL='d';", "P0001", "P0001", "d"},
		{"numeric", "RAISE USING MESSAGE=7,DETAIL=true;", "P0001", "7", "t"},
		{"empty", "RAISE USING MESSAGE='',DETAIL='';", "P0001", "", ""},
		{"duplicate", "RAISE USING DETAIL='a',DETAIL='b';", "42601", "RAISE option already specified: DETAIL", ""},
		{"empty_duplicate", "RAISE USING DETAIL='',DETAIL='b';", "42601", "RAISE option already specified: DETAIL", ""},
		{"null_before_duplicate", "RAISE USING DETAIL='a',DETAIL=NULL;", "22004", "RAISE statement option cannot be null", ""},
		{"error_before_duplicate", "RAISE USING DETAIL='a',DETAIL=1/0;", "22012", "division by zero", ""},
		{"format_message", "RAISE 'x' USING MESSAGE='y';", "42601", "RAISE option already specified: MESSAGE", ""},
		{"format_message_null", "RAISE 'x' USING MESSAGE=NULL;", "22004", "RAISE statement option cannot be null", ""},
		{"condition_code", "RAISE division_by_zero USING ERRCODE='ZX987';", "42601", "RAISE option already specified: ERRCODE", ""},
		{"condition_code_null", "RAISE division_by_zero USING ERRCODE=NULL;", "22004", "RAISE statement option cannot be null", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn) {
				want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "inline_code_block", "RAISE")
				want.Detail = tc.detail
				if tc.name == "error_before_duplicate" {
					want.Context = "PL/pgSQL expression \"1/0\"\n" + want.Context
				}
				plpgsqlError(t, p, c, raiseAssertDO(tc.body), false, want)
			})
		})
	}
	for _, option := range []string{"MESSAGE", "DETAIL", "HINT", "ERRCODE", "COLUMN", "CONSTRAINT", "DATATYPE", "TABLE", "SCHEMA"} {
		t.Run("null_"+option, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn) {
				raiseAssertError(t, p, c, "RAISE USING "+option+"=NULL;", "22004", "RAISE statement option cannot be null", "RAISE")
			})
		})
		if option != "ERRCODE" {
			t.Run("duplicate_"+option, func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn) {
					raiseAssertError(t, p, c, "RAISE USING "+option+"='',"+option+"='x';", "42601", "RAISE option already specified: "+option, "RAISE")
				})
			})
		}
	}
	t.Run("unknown_option", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn) {
			want := fixture.ControlsReferenceDiagnostic("42601", "unrecognized RAISE statement option at or near \"bogus\"")
			want.Position = fixture.Position(24)
			plpgsqlError(t, p, c, raiseAssertDO("RAISE USING bogus='x';"), false, want)
		})
	})
	t.Run("ordered_once", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
			var records []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &records)
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE SEQUENCE raise_option_seq", 0))
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("ALTER SEQUENCE raise_option_seq RESTART WITH 1", 0))
				records = nil
				s := fixture.Exec(raiseAssertDO("RAISE NOTICE '%',nextval('raise_option_seq') USING DETAIL=nextval('raise_option_seq'),HINT=nextval('raise_option_seq');"), 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, rec, s)
				want := raiseAssertNotice("NOTICE", "00000", "1", "inline_code_block")
				want.Detail, want.Hint = "2", "3"
				raiseAssertNotices(t, records, []fixture.Diagnostic{want})
				fixture.RunStep(t, p.Ctx, c, rec, fixture.Select("SELECT last_value AS v FROM raise_option_seq", []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
			}
		})
	})
}

func TestReferencePLpgSQLRaiseSeverity(t *testing.T) {
	for _, threshold := range []string{"debug1", "notice", "error"} {
		t.Run(threshold, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
				var records []fixture.Diagnostic
				_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &records)
				fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("SET client_min_messages TO "+threshold, 0))
				fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE FUNCTION raise_levels() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RAISE DEBUG 'debug'; RAISE LOG 'log'; RAISE INFO 'info'; RAISE NOTICE 'notice'; RAISE WARNING 'warning'; RETURN 7; END$$", 0))
				for _, prepared := range []bool{false, true} {
					records = nil
					s := fixture.Select("SELECT raise_levels() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, rec, s)
					var want []fixture.Diagnostic
					for _, level := range []string{"DEBUG", "LOG", "INFO", "NOTICE", "WARNING"} {
						if threshold != "debug1" && (level == "DEBUG" || level == "LOG") || threshold == "error" && level != "INFO" {
							continue
						}
						code := "00000"
						if level == "WARNING" { code = "01000" }
						want = append(want, raiseAssertNotice(level, code, strings.ToLower(level), "raise_levels()"))
					}
					raiseAssertNotices(t, records, want)
				}
			})
		})
	}
	t.Run("severity_independent_of_code", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
			var records []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &records)
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE FUNCTION raise_codes() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RAISE NOTICE SQLSTATE '22012'; RAISE NOTICE SQLSTATE '00000'; RAISE WARNING USING HINT='h'; RETURN 7; END$$", 0))
			for _, prepared := range []bool{false, true} {
				records = nil
				s := fixture.Select("SELECT raise_codes() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, rec, s)
				warning := raiseAssertNotice("WARNING", "01000", "00000", "raise_codes()")
				warning.Hint = "h"
				raiseAssertNotices(t, records, []fixture.Diagnostic{raiseAssertNotice("NOTICE", "22012", "22012", "raise_codes()"), raiseAssertNotice("NOTICE", "00000", "00000", "raise_codes()"), warning})
			}
			raiseAssertError(t, p, c, "RAISE EXCEPTION 'boom';", "P0001", "boom", "RAISE")
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("SET client_min_messages TO error", 0))
			want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "inline_code_block", "RAISE")
			want.Context = "PL/pgSQL expression \"1/0\"\n" + want.Context
			plpgsqlError(t, p, c, raiseAssertDO("RAISE DEBUG '%',1/0;"), false, want)
		})
	})
}
