package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// ProcedureCreate owns catalog safety; plpgsql_validator owns the additional
// language signature restrictions. An omitted validator must not run the latter.
func TestReferenceRoutineLanguagesSignatureValidation(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_sig_deferred,rl_sig_checked CASCADE")()
		userOperatorExec(t, p, b, c,
			"CREATE TRUSTED LANGUAGE rl_sig_deferred"+routineLanguageCall,
			"CREATE TRUSTED LANGUAGE rl_sig_checked"+routineLanguageCall+" VALIDATOR pg_catalog.plpgsql_validator")
		for _, language := range []string{"rl_sig_deferred", "rl_sig_checked"} {
			for _, tc := range []struct{ name, result, message, detail string }{
				{"poly", "anyelement", "cannot determine result data type", "A result of type anyelement requires at least one input of type anyelement, anyarray, anynonarray, anyenum, anyrange, or anymultirange."},
				{"internal", "internal", `unsafe use of pseudo-type "internal"`, "A result of type internal requires at least one input of type internal."},
			} {
				name := language + "_" + tc.name
				d := fixture.ControlsReferenceDiagnostic("42P13", tc.message)
				d.Detail = tc.detail
				routineLanguagesError(t, p, b, c, "CREATE FUNCTION "+name+"() RETURNS "+tc.result+" LANGUAGE "+language+" AS 'BEGIN RETURN NULL; END'", d, false, "execute")
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='"+name+"'", "INT8", int64(0))
			}
			userOperatorExec(t, p, b, c, "CREATE FUNCTION "+language+"_good(x integer) RETURNS integer LANGUAGE "+language+" AS $$BEGIN RETURN x+1; END$$")
			parametersValue(t, p, b, c, "SELECT "+language+"_good($1::integer) AS v", "v", "INT4", int64(5), 4)
			parametersHeld(t, p, b, c, "SELECT "+language+"_good($1::integer) AS v", "INT4", [][]any{{4}, {9}}, []any{int64(5), int64(10)})
		}
		for _, tc := range []struct{ name, signature, result, message string }{
			{"result", "()", "cstring", "PL/pgSQL functions cannot return type cstring"},
			{"argument", "(x cstring)", "integer", "PL/pgSQL functions cannot accept type cstring"},
		} {
			d := fixture.ControlsReferenceDiagnostic("0A000", tc.message)
			routineLanguagesError(t, p, b, c, "CREATE FUNCTION rl_sig_checked_"+tc.name+tc.signature+" RETURNS "+tc.result+" LANGUAGE rl_sig_checked AS 'BEGIN RETURN NULL; END'", d, false, "execute")
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rl_sig_checked_"+tc.name+"'", "INT8", int64(0))
		}
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION rl_sig_result() RETURNS cstring LANGUAGE rl_sig_deferred AS 'BEGIN RETURN NULL; END'",
			"CREATE FUNCTION rl_sig_argument(x cstring) RETURNS integer LANGUAGE rl_sig_deferred AS 'BEGIN RETURN NULL; END'")
		routineLanguagesSignatureState(t, p, b, c)
		// Adding a genuine validator does not retroactively validate declarations.
		// Reached compilation must still reject them without mutating publication.
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRUSTED LANGUAGE rl_sig_deferred"+routineLanguageCall+" VALIDATOR pg_catalog.plpgsql_validator")
		routineLanguagesSignatureState(t, p, b, c)
	})
}

func TestReferenceRoutineLanguagesSignaturePersistence(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_sig_deferred CASCADE")()
		userOperatorExec(t, p, b, c,
			"CREATE TRUSTED LANGUAGE rl_sig_deferred"+routineLanguageCall,
			"CREATE FUNCTION rl_sig_result() RETURNS cstring LANGUAGE rl_sig_deferred AS 'BEGIN RETURN NULL; END'",
			"CREATE FUNCTION rl_sig_argument(x cstring) RETURNS integer LANGUAGE rl_sig_deferred AS 'BEGIN RETURN NULL; END'",
			"CREATE FUNCTION rl_sig_good(x integer) RETURNS integer LANGUAGE rl_sig_deferred AS $$BEGIN RETURN x+1; END$$")
		check := func(c *sql.Conn, b fixture.Backend) {
			routineLanguagesSignatureState(t, p, b, c)
			parametersValue(t, p, b, c, "SELECT rl_sig_good($1::integer) AS v", "v", "INT4", int64(5), 4)
			parametersHeld(t, p, b, c, "SELECT rl_sig_good($1::integer) AS v", "INT4", [][]any{{4}, {9}}, []any{int64(5), int64(10)})
		}
		check(c, b)
		if !b.PG {
			routineLanguagesExtensionReplay(t, p, b, c, nil, "rl_sig_good", check)
		}
	})
}

func routineLanguagesSignatureState(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	catalog := fixture.Select("SELECT p.proname,t.typname,p.pronargs,p.prosrc,l.lanname FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang JOIN pg_type t ON t.oid=p.prorettype WHERE p.proname IN ('rl_sig_result','rl_sig_argument') ORDER BY p.proname",
		[]string{"proname", "typname", "pronargs", "prosrc", "lanname"}, []string{"NAME", "NAME", "INT2", "TEXT", "NAME"},
		[][]any{{"rl_sig_argument", "int4", int64(1), "BEGIN RETURN NULL; END", "rl_sig_deferred"}, {"rl_sig_result", "cstring", int64(0), "BEGIN RETURN NULL; END", "rl_sig_deferred"}})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, catalog)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(catalog))
	plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rl_sig_argument' AND proargtypes[0]='cstring'::regtype", "INT8", int64(1))
	for _, tc := range []struct{ name, query, message string }{
		{"rl_sig_result", "SELECT rl_sig_result()", "PL/pgSQL functions cannot return type cstring"},
		{"rl_sig_argument", "SELECT rl_sig_argument(NULL::cstring)", "PL/pgSQL functions cannot accept type cstring"},
	} {
		d := fixture.ControlsReferenceDiagnostic("0A000", tc.message)
		d.Context = "compilation of PL/pgSQL function \"" + tc.name + "\" near line 1"
		routineLanguagesError(t, p, b, c, tc.query, d, true, "execute")
	}
	plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname IN ('rl_sig_result','rl_sig_argument')", "INT8", int64(2))
}
