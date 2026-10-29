package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineLanguagesInvocation(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer routineLanguagesCleanup(t, c, `DROP LANGUAGE IF EXISTS "RL Full",rl_call,rl_inline,rl_validate CASCADE`)()
		userOperatorExec(t, p, b, c,
			`CREATE TRUSTED PROCEDURAL LANGUAGE "RL Full"`+routineLanguageFull,
			"SET search_path=pg_catalog,public",
			"CREATE TRUSTED LANGUAGE RL_CALL HANDLER plpgsql_call_handler",
			"CREATE TRUSTED LANGUAGE rl_inline"+routineLanguageCall+" INLINE pg_catalog.plpgsql_inline_handler",
			"CREATE TRUSTED LANGUAGE rl_validate"+routineLanguageCall+" VALIDATOR pg_catalog.plpgsql_validator",
			"RESET search_path",
			"CREATE FUNCTION rl_sql(x integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1+1'",
			`CREATE FUNCTION rl_full_fn(x integer) RETURNS integer LANGUAGE "RL Full" AS $$BEGIN RETURN x+1; END$$`,
			"CREATE FUNCTION rl_call_fn(x integer) RETURNS integer LANGUAGE RL_CALL AS $$BEGIN RETURN x+1; END$$",
			"CREATE FUNCTION rl_inline_fn(x integer) RETURNS integer LANGUAGE rl_inline AS $$BEGIN RETURN x+1; END$$",
			"CREATE FUNCTION rl_validate_fn(x integer) RETURNS integer LANGUAGE rl_validate AS $$BEGIN RETURN x+1; END$$",
			"CREATE TABLE rl_inline_effect(v integer)",
			`DO LANGUAGE "RL Full" $$BEGIN INSERT INTO rl_inline_effect VALUES(5); END$$`,
			"DO LANGUAGE rl_inline $$BEGIN INSERT INTO rl_inline_effect VALUES(10); END$$")
		for _, fn := range []string{"rl_sql", "rl_full_fn", "rl_call_fn", "rl_inline_fn", "rl_validate_fn"} {
			parametersValue(t, p, b, c, "SELECT "+fn+"($1::integer) AS v", "v", "INT4", int64(5), 4)
			parametersHeld(t, p, b, c, "SELECT "+fn+"($1::integer) AS v", "INT4", [][]any{{4}, {9}}, []any{int64(5), int64(10)})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM rl_inline_effect ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(10)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT l.lanname,l.lanispl,l.lanpltrusted,h.proname,i.proname,v.proname,l.lanacl IS NULL AS default_acl FROM pg_language l JOIN pg_proc h ON h.oid=l.lanplcallfoid LEFT JOIN pg_proc i ON i.oid=l.laninline LEFT JOIN pg_proc v ON v.oid=l.lanvalidator WHERE l.lanname IN ('RL Full','rl_call','rl_inline','rl_validate') ORDER BY l.lanname`,
			[]string{"lanname", "lanispl", "lanpltrusted", "proname", "proname", "proname", "default_acl"}, []string{"NAME", "BOOL", "BOOL", "NAME", "NAME", "NAME", "BOOL"},
			[][]any{{"RL Full", true, true, "plpgsql_call_handler", "plpgsql_inline_handler", "plpgsql_validator", true}, {"rl_call", true, true, "plpgsql_call_handler", nil, nil, true}, {"rl_inline", true, true, "plpgsql_call_handler", "plpgsql_inline_handler", nil, true}, {"rl_validate", true, true, "plpgsql_call_handler", nil, "plpgsql_validator", true}}))
		plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.proname IN ('rl_full_fn','rl_call_fn','rl_inline_fn','rl_validate_fn') AND l.lanname IN ('RL Full','rl_call','rl_inline','rl_validate')", "INT8", int64(4))
		// Exercise the integrated RETURN QUERY EXECUTE set transport through
		// a source-only alias, independently of optional CREATE validation.
		// Language registration does not add another PL statement runtime.
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION rl_call_set(x integer) RETURNS SETOF integer LANGUAGE rl_call AS $$BEGIN RETURN QUERY EXECUTE 'SELECT $1::integer+1 UNION ALL SELECT $1::integer+2' USING x; END$$",
			"CREATE PROCEDURE rl_call_out(IN x integer, OUT y integer, OUT z integer) LANGUAGE rl_call AS $$BEGIN y:=x+1; z:=x+2; END$$")
		for _, prepared := range []bool{false, true} {
			set := fixture.Select("SELECT rl_call_set($1::integer) AS v ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(6)}}, 4)
			set.Prepared = prepared
			set.CheckMetadata = parametersMetadata(b, "v", "INT4")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, set)
			output := fixture.Select("CALL rl_call_out(4,NULL,NULL)", []string{"y", "z"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(6)}})
			output.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, output)
		}
		for _, name := range []string{"rl_call", "rl_validate"} {
			routineLanguagesError(t, p, b, c, "DO LANGUAGE "+name+" $$BEGIN END$$", fixture.ControlsReferenceDiagnostic("0A000", `language "`+name+`" does not support inline code execution`), false, "execute")
		}
	})
}

func TestReferenceRoutineLanguagesHandlers(t *testing.T) {
	for _, tc := range []struct{ name, suffix, missing string }{
		{"call", " HANDLER rl_absent", "rl_absent()"},
		{"inline", routineLanguageCall + " INLINE rl_absent", "rl_absent(internal)"},
		{"validator", routineLanguageCall + " VALIDATOR rl_absent", "rl_absent(oid)"},
		{"arity", " HANDLER public.rl_arity", "public.rl_arity()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_handler CASCADE")()
				userOperatorExec(t, p, b, c, "CREATE FUNCTION rl_arity(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'")
				d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.missing+" does not exist")
				routineLanguagesError(t, p, b, c, "CREATE LANGUAGE rl_handler"+tc.suffix, d, false, "execute")
				routineLanguagesCount(t, p, b, c, "rl_handler", 0)
				userOperatorExec(t, p, b, c, "CREATE TRUSTED LANGUAGE rl_handler"+routineLanguageFull)
				// Exact lookup precedes duplicate detection even for an existing language.
				routineLanguagesError(t, p, b, c, "CREATE LANGUAGE rl_handler"+tc.suffix, d, false, "execute")
				routineLanguagesCount(t, p, b, c, "rl_handler", 1)
			})
		})
	}
	t.Run("result_and_replacement", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_handler CASCADE")()
			userOperatorExec(t, p, b, c, "CREATE FUNCTION rl_wrong() RETURNS integer LANGUAGE SQL AS 'SELECT 5'")
			routineLanguagesError(t, p, b, c, "CREATE LANGUAGE rl_handler HANDLER rl_wrong", fixture.ControlsReferenceDiagnostic("42809", "function rl_wrong must return type language_handler"), false, "execute")
			routineLanguagesCount(t, p, b, c, "rl_handler", 0)
			userOperatorExec(t, p, b, c, "CREATE TRUSTED LANGUAGE rl_handler"+routineLanguageFull)
			oid := routineLanguagesOID(t, p, c, "rl_handler")
			routineLanguagesError(t, p, b, c, "CREATE TRUSTED LANGUAGE rl_handler"+routineLanguageFull, fixture.ControlsReferenceDiagnostic("42710", `language "rl_handler" already exists`), false, "execute")
			userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRUSTED LANGUAGE rl_handler"+routineLanguageCall)
			if got := routineLanguagesOID(t, p, c, "rl_handler"); got != oid {
				t.Fatalf("replacement changed language identity: %d -> %d", oid, got)
			}
			plpgsqlValue(t, p, b, c, "SELECT laninline=0 AND lanvalidator=0 AS v FROM pg_language WHERE lanname='rl_handler'", "BOOL", true)
		})
	})
	t.Run("search_path_shadow", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_shadow CASCADE")()
			userOperatorExec(t, p, b, c, "CREATE FUNCTION public.plpgsql_call_handler() RETURNS integer LANGUAGE SQL AS 'SELECT 7'", "SET search_path=public,pg_catalog")
			routineLanguagesError(t, p, b, c, "CREATE LANGUAGE rl_shadow HANDLER plpgsql_call_handler", fixture.ControlsReferenceDiagnostic("42809", "function plpgsql_call_handler must return type language_handler"), false, "execute")
			routineLanguagesCount(t, p, b, c, "rl_shadow", 0)
			userOperatorExec(t, p, b, c, "CREATE TRUSTED LANGUAGE rl_shadow"+routineLanguageFull, "RESET search_path", "CREATE FUNCTION rl_shadow_fn(x integer) RETURNS integer LANGUAGE rl_shadow AS $$BEGIN RETURN x+1; END$$")
			plpgsqlValue(t, p, b, c, "SELECT rl_shadow_fn(4) AS v", "INT4", int64(5))
		})
	})
	t.Run("unavailable_optional_provider", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_external CASCADE")()
			userOperatorExec(t, p, b, c, "CREATE FUNCTION rl_not_validator(oid) RETURNS integer LANGUAGE SQL AS 'SELECT 7'")
			q := "CREATE TRUSTED LANGUAGE rl_external" + routineLanguageCall + " VALIDATOR rl_not_validator"
			// PostgreSQL ignores optional handler result types and can invoke SQL
			// functions as validators. Native cannot claim that ABI capability.
			if b.PG {
				userOperatorExec(t, p, b, c, q, "CREATE FUNCTION rl_external_fn(x integer) RETURNS integer LANGUAGE rl_external AS $$BEGIN RETURN x+1; END$$")
				plpgsqlValue(t, p, b, c, "SELECT rl_external_fn(4) AS v", "INT4", int64(5))
			} else {
				routineLanguagesError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("0A000", "language validator has no implemented native capability"), false, "execute")
				routineLanguagesCount(t, p, b, c, "rl_external", 0)
			}
		})
	})
}

func TestReferenceRoutineLanguagesValidationTiming(t *testing.T) {
	for _, validated := range []bool{false, true} {
		name := "deferred"
		if validated {
			name = "validated"
		}
		t.Run(name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_timing CASCADE")()
				suffix := routineLanguageCall
				if validated {
					suffix += " VALIDATOR pg_catalog.plpgsql_validator"
				}
				userOperatorExec(t, p, b, c, "CREATE TRUSTED LANGUAGE rl_timing"+suffix,
					"CREATE FUNCTION rl_timing_good(x integer) RETURNS integer LANGUAGE rl_timing AS $$BEGIN RETURN x+1; END$$")
				parametersHeld(t, p, b, c, "SELECT rl_timing_good($1::integer) AS v", "INT4", [][]any{{4}, {9}}, []any{int64(5), int64(10)})
				q := "CREATE FUNCTION rl_timing_bad() RETURNS integer LANGUAGE rl_timing AS 'BEGIN RETURN 1;'"
				d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
				d.InternalPosition, d.InternalQuery = "16", "BEGIN RETURN 1;"
				d.Context = "compilation of PL/pgSQL function \"rl_timing_bad\" near line 1"
				if validated {
					// CREATE maps the body location into the original statement;
					// invocation instead reports the internal compiler context.
					d.Position = "87"
					d.InternalPosition, d.InternalQuery, d.Context = "", "", ""
					routineLanguagesError(t, p, b, c, q, d, false, "execute")
					plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rl_timing_bad'", "INT8", int64(0))
				} else {
					userOperatorExec(t, p, b, c, q)
					routineLanguagesError(t, p, b, c, "SELECT rl_timing_bad()", d, true, "execute")
				}
			})
		})
	}
}
