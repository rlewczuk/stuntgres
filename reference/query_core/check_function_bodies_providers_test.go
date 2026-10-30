package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCheckFunctionBodiesProviders(t *testing.T) {
	t.Run("SQL forward function", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			body := "SELECT x+1 FROM cfb_later"
			q := "CREATE FUNCTION cfb_forward() RETURNS integer LANGUAGE SQL AS $$" + body + "$$"
			d := fixture.ControlsReferenceDiagnostic("42P01", `relation "cfb_later" does not exist`)
			d.Position = "81"
			checkBodiesFailure(t, p, b, c, q, d, false)
			checkBodiesCount(t, p, b, c, "cfb_forward", 0)
			routineExec(t, p, b, c, "SET check_function_bodies=off", q)
			runtime := fixture.ControlsReferenceDiagnostic("42P01", d.Message)
			runtime.InternalPosition, runtime.InternalQuery = "17", body
			runtime.Context = `SQL function "cfb_forward" during inlining`
			checkBodiesFailure(t, p, b, c, "SELECT cfb_forward()", runtime, true)
			routineExec(t, p, b, c, "SET check_function_bodies=on", "CREATE TABLE cfb_later(x integer)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cfb_later VALUES (41)", 1))
			routineValue(t, p, b, c, "SELECT cfb_forward() AS v", "v", "INT4", int64(42))
		})
	})
	t.Run("SQL forward procedure", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			body := "INSERT INTO cfb_later_sink VALUES (42)"
			q := "CREATE PROCEDURE cfb_forward_proc() LANGUAGE SQL AS $$" + body + "$$"
			d := fixture.ControlsReferenceDiagnostic("42P01", `relation "cfb_later_sink" does not exist`)
			d.Position = "67"
			checkBodiesFailure(t, p, b, c, q, d, false)
			checkBodiesCount(t, p, b, c, "cfb_forward_proc", 0)
			routineExec(t, p, b, c, "SET check_function_bodies=off", q)
			runtime := fixture.ControlsReferenceDiagnostic("42P01", d.Message)
			runtime.Context = `SQL function "cfb_forward_proc" statement 1`
			runtime.InternalPosition, runtime.InternalQuery = "13", body
			checkBodiesFailure(t, p, b, c, "CALL cfb_forward_proc()", runtime, false)
			routineExec(t, p, b, c, "CREATE TABLE cfb_later_sink(x integer)")
			routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM cfb_later_sink", "v", "INT8", int64(0), false, true)
			routineExec(t, p, b, c, "SET check_function_bodies=on", "CALL cfb_forward_proc()")
			routineValueWithNativeNullability(t, p, b, c, "SELECT x AS v FROM cfb_later_sink", "v", "INT4", int64(42), true, true)
		})
	})
	for _, language := range []string{"SQL", "plpgsql"} {
		for _, kind := range []string{"FUNCTION", "PROCEDURE"} {
			t.Run("malformed_"+language+"_"+kind, func(t *testing.T) {
				routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					body, message := "SELECT 1 +", "syntax error at end of input"
					if language == "plpgsql" {
						body, message = "BEGIN WHILE LOOP END LOOP; END", `missing expression at or near "LOOP"`
					}
					q := "CREATE " + kind + " cfb_malformed()"
					call := "CALL cfb_malformed()"
					query := false
					if kind == "FUNCTION" {
						q += " RETURNS integer"
						call, query = "SELECT cfb_malformed()", true
					}
					q += " LANGUAGE " + language + " AS $$" + body + "$$"
					d := fixture.ControlsReferenceDiagnostic("42601", message)
					bodyStart := strings.Index(q, "$$") + 2
					d.Position = strconv.Itoa(bodyStart + len(body) + 1)
					if language == "plpgsql" {
						d.Position = strconv.Itoa(bodyStart + strings.Index(body, "LOOP") + 1)
					}
					checkBodiesFailure(t, p, b, c, q, d, false)
					checkBodiesCount(t, p, b, c, "cfb_malformed", 0)
					routineExec(t, p, b, c, "SET check_function_bodies=off", q)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT prosrc FROM pg_proc WHERE proname='cfb_malformed' AND pronamespace='public'::regnamespace", []string{"prosrc"}, []string{"TEXT"}, [][]any{{body}}))
					runtime := fixture.ControlsReferenceDiagnostic("42601", message)
					if language == "SQL" {
						runtime.Context = `SQL function "cfb_malformed" during startup`
						if kind == "FUNCTION" {
							runtime.Context = `SQL function "cfb_malformed" during inlining`
						}
						runtime.InternalPosition, runtime.InternalQuery = "11", body
					} else {
						runtime.InternalPosition, runtime.InternalQuery = "13", body
						runtime.Context = `compilation of PL/pgSQL function "cfb_malformed" near line 1`
					}
					checkBodiesFailure(t, p, b, c, call, runtime, query)
					routineExec(t, p, b, c, "SET check_function_bodies=on")
					checkBodiesCount(t, p, b, c, "cfb_malformed", 1)
					checkBodiesFailure(t, p, b, c, call, runtime, query)
					if !b.PG {
						// Native persistence must round-trip genuine unchecked source,
						// not reject it or manufacture a checked program on replay.
						routineKind, result := "f", "integer"
						if kind == "PROCEDURE" {
							routineKind, result = "p", "void"
						}
						declaration := fixture.Select("SELECT prosrc,prokind,prorettype::regtype::text,pronargs,proconfig::text FROM pg_proc WHERE proname='cfb_malformed' AND pronamespace='public'::regnamespace",
							[]string{"prosrc", "prokind", "prorettype", "pronargs", "proconfig"}, []string{"TEXT", "CHAR", "TEXT", "INT2", "TEXT"}, [][]any{{body, routineKind, result, int64(0), nil}})
						fixture.RunStep(t, p.Ctx, c, b.Recorder, declaration)
						checkBodiesReads(t, p, b, c, "on")
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal("source-only routine replay", err)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, declaration)
						checkBodiesReads(t, p, b, c, "on")
						checkBodiesFailure(t, p, b, c, call, runtime, query)
					}
				})
			})
		}
	}
	t.Run("PL deferred SQL names", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			// PL validation compiles PL structure, not every reached SQL name.
			routineExec(t, p, b, c,
				"CREATE FUNCTION cfb_pl_forward() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN (SELECT x+1 FROM cfb_pl_later); END$$",
				"CREATE PROCEDURE cfb_pl_proc() LANGUAGE plpgsql AS $$BEGIN INSERT INTO cfb_pl_sink VALUES (42); END$$",
				"CREATE TABLE cfb_pl_later(x integer)", "CREATE TABLE cfb_pl_sink(x integer)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cfb_pl_later VALUES (41)", 1))
			routineValue(t, p, b, c, "SELECT cfb_pl_forward() AS v", "v", "INT4", int64(42))
			routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM cfb_pl_sink", "v", "INT8", int64(0), false, true)
			routineExec(t, p, b, c, "CALL cfb_pl_proc()")
			routineValueWithNativeNullability(t, p, b, c, "SELECT x AS v FROM cfb_pl_sink", "v", "INT4", int64(42), true, true)
		})
	})
	t.Run("declaration admission", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c, "SET check_function_bodies=off")
			for _, language := range []string{"SQL", "plpgsql"} {
				d := fixture.ControlsReferenceDiagnostic("42P13", `unsafe use of pseudo-type "internal"`)
				d.Detail = "A result of type internal requires at least one input of type internal."
				checkBodiesFailure(t, p, b, c, "CREATE FUNCTION cfb_unsafe() RETURNS internal LANGUAGE "+language+" AS 'invalid body'", d, false)
				checkBodiesCount(t, p, b, c, "cfb_unsafe", 0)
			}
			q := "CREATE FUNCTION cfb_bad_type(x cfb_unknown_type) RETURNS integer LANGUAGE SQL AS 'invalid body'"
			d := fixture.ControlsReferenceDiagnostic("42704", `type cfb_unknown_type does not exist`)
			d.Position = strconv.Itoa(strings.Index(q, "cfb_unknown_type") + 1)
			checkBodiesFailure(t, p, b, c, q, d, false)
			checkBodiesCount(t, p, b, c, "cfb_bad_type", 0)
			q = "CREATE FUNCTION cfb_bad_default(x integer DEFAULT 'broken') RETURNS integer LANGUAGE SQL AS $$SELECT x$$"
			d = fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "broken"`)
			d.Position = strconv.Itoa(strings.Index(q, "'broken'") + 1)
			checkBodiesFailure(t, p, b, c, q, d, false)
			checkBodiesCount(t, p, b, c, "cfb_bad_default", 0)
			checkBodiesFailure(t, p, b, c, "CREATE FUNCTION cfb_bad_signature() RETURNS cstring LANGUAGE plpgsql AS 'invalid body'", fixture.ControlsReferenceDiagnostic("0A000", "PL/pgSQL functions cannot return type cstring"), false)
			checkBodiesCount(t, p, b, c, "cfb_bad_signature", 0)
		})
	})
}

func TestReferenceCheckFunctionBodiesFeatureSmoke(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		checkBodiesReads(t, p, b, c, "on")
		body := "SELECT x+1 FROM cfb_smoke_source"
		q := "CREATE FUNCTION cfb_smoke() RETURNS integer LANGUAGE SQL AS $$" + body + "$$"
		d := fixture.ControlsReferenceDiagnostic("42P01", `relation "cfb_smoke_source" does not exist`)
		d.Position = strconv.Itoa(strings.Index(q, "cfb_smoke_source") + 1)
		checkBodiesFailure(t, p, b, c, q, d, false)
		checkBodiesCount(t, p, b, c, "cfb_smoke", 0)
		routineExec(t, p, b, c, "SET check_function_bodies=off", q,
			"CREATE PROCEDURE cfb_smoke_proc() LANGUAGE SQL AS $$INSERT INTO cfb_smoke_sink SELECT cfb_smoke()$$")
		checkBodiesReads(t, p, b, c, "off")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT setting FROM pg_settings WHERE name='check_function_bodies'", []string{"setting"}, []string{"TEXT"}, [][]any{{"off"}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT prosrc FROM pg_proc WHERE oid='cfb_smoke()'::regprocedure", []string{"prosrc"}, []string{"TEXT"}, [][]any{{body}}))
		routineExec(t, p, b, c, "CREATE TABLE cfb_smoke_source(x integer)", "CREATE TABLE cfb_smoke_sink(x integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cfb_smoke_source VALUES (41)", 1))
		routineValue(t, p, b, c, "SELECT cfb_smoke() AS v", "v", "INT4", int64(42))
		routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM cfb_smoke_sink", "v", "INT8", int64(0), false, true)
		routineExec(t, p, b, c, "CALL cfb_smoke_proc()")
		routineValueWithNativeNullability(t, p, b, c, "SELECT x AS v FROM cfb_smoke_sink", "v", "INT4", int64(42), true, true)
		routineExec(t, p, b, c, "RESET check_function_bodies")
		checkBodiesReads(t, p, b, c, "on")
		checkBodiesFailure(t, p, b, c, checkBodiesMismatch("cfb_smoke_rejected"), checkBodiesReturnDiagnostic("cfb_smoke_rejected", false), false)
		checkBodiesCount(t, p, b, c, "cfb_smoke_rejected", 0)
		routineValue(t, p, b, c, "SELECT cfb_smoke() AS v", "v", "INT4", int64(42))
	})
}
