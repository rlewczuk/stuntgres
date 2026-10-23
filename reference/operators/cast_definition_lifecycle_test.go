package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCastDefinitionContextPrecedence(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			for _, q := range []string{castDefinitionSource, `CREATE FUNCTION cast_score(cast_src) RETURNS integer LANGUAGE SQL AS $$ SELECT CASE WHEN $1 IS NULL THEN 99 ELSE 41 END $$`,
				"CREATE FUNCTION cast_label(cast_src) RETURNS text LANGUAGE SQL AS $$ SELECT 'custom'::text $$",
				"CREATE TABLE labels(v text)", "CREATE CAST (cast_src AS text) WITH FUNCTION cast_label(cast_src)",
				castDefinitionEdge + " AS IMPLICIT",
				"CREATE FUNCTION cast_truth(cast_src) RETURNS boolean LANGUAGE SQL AS $$ SELECT true $$",
				"CREATE CAST (cast_src AS boolean) WITH FUNCTION cast_truth(cast_src) AS IMPLICIT",
				"CREATE FUNCTION cast_choose(integer) RETURNS integer LANGUAGE SQL AS $$ SELECT 1 $$",
				"CREATE FUNCTION cast_choose(boolean) RETURNS integer LANGUAGE SQL AS $$ SELECT 2 $$"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Select("SELECT ('red'::cast_src)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"custom"}}))
			s := fixture.ErrorDiagnostic("INSERT INTO labels VALUES ('red'::cast_src)", fixture.ControlsReferenceDiagnostic("42804", `column "v" is of type text but expression is of type cast_src`))
			s.Diagnostic.Hint = "You will need to rewrite or cast the expression."
			s.Diagnostic.Position = "28"
			run(s)
			s = fixture.ErrorDiagnostic("SELECT cast_choose('red'::cast_src)", fixture.ControlsReferenceDiagnostic("42725", "function cast_choose(cast_src) is not unique"))
			s.Diagnostic.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
			s.Diagnostic.Position = "8"
			run(s)
			run(fixture.Exec("CREATE FUNCTION cast_choose(cast_src) RETURNS integer LANGUAGE SQL AS $$ SELECT 3 $$", 0))
			run(fixture.Select("SELECT cast_choose('red'::cast_src) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
			// Operator binding admits the edge; common-type selection still
			// rejects different type categories before considering coercion.
			run(fixture.Select("SELECT ('red'::cast_src)+1 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			common := fixture.ControlsReferenceDiagnostic("42804", "CASE types integer and cast_src cannot be matched")
			common.Position = "28"
			run(fixture.ErrorDiagnostic("SELECT CASE WHEN true THEN 'red'::cast_src ELSE 2 END AS v", common))
			common = fixture.ControlsReferenceDiagnostic("42804", "COALESCE types integer and cast_src cannot be matched")
			common.Position = "31"
			run(fixture.ErrorDiagnostic("SELECT COALESCE(NULL::integer,'red'::cast_src) AS v", common))
			// No reverse inference or arbitrary cast graph composition.
			d := fixture.ControlsReferenceDiagnostic("42846", "cannot cast type integer to cast_src")
			d.Position = "21"
			run(fixture.ErrorDiagnostic("SELECT (41::integer)::cast_src", d))
			run(fixture.Exec("CREATE TYPE cast_last AS ENUM ('red')", 0))
			run(fixture.Exec("CREATE CAST (text AS cast_last) WITH INOUT AS IMPLICIT", 0))
			d = fixture.ControlsReferenceDiagnostic("42846", "cannot cast type cast_src to cast_last")
			d.Position = "25"
			run(fixture.ErrorDiagnostic("SELECT ('red'::cast_src)::cast_last", d))
			// Unknown literal input is not the declared TEXT edge.
			run(fixture.Select("SELECT 'red'::cast_src::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
		})
	}
}

func TestReferenceCastDefinitionPreparedLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			castDefinitionCleanup(t, p, backend, [2]string{"xid", "real"})
			for _, q := range []string{castDefinitionSource, castDefinitionScore, "CREATE SEQUENCE cast_calls"} {
				run(fixture.Exec(q, 0))
			}
			stmt, err := conn.PrepareContext(p.Ctx, castDefinitionEdge)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_src'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: castDefinitionEdge}, backend.Recorder, fixture.Exec(castDefinitionEdge, 0))
			q := "SELECT ($1::cast_src)::integer AS v"
			held, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			exec := fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}
			for _, tc := range []struct {
				in  any
				out int64
			}{{"red", 41}, {"blue", 42}, {nil, 99}, {"red", 41}} {
				fixture.RunStep(t, p.Ctx, exec, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{tc.out}}, tc.in))
			}
			run(fixture.Exec("CREATE CAST (xid AS real) WITHOUT FUNCTION", 0))
			fixture.RunStep(t, p.Ctx, exec, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}, "blue"))
			run(fixture.Exec("DROP CAST (cast_src AS integer)", 0))
			run(fixture.Exec("CREATE FUNCTION cast_new(cast_src) RETURNS integer LANGUAGE SQL AS $$ SELECT 77 $$", 0))
			run(fixture.Exec("CREATE CAST (cast_src AS integer) WITH FUNCTION cast_new(cast_src)", 0))
			// tests-1: PostgreSQL retains the analyzed routine (41); native
			// private analysis invalidates the changed pair (77), not a PG plan.
			want := int64(77)
			if backend.PG {
				want = 41
			}
			fixture.RunStep(t, p.Ctx, exec, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{want}}, "red"))
			run(fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}, "red"))
			// Native also refreshes a previously absent pair; PG retains IO.
			fallbackQ := "SELECT ($1::cast_src)::text AS v"
			fallback, err := conn.PrepareContext(p.Ctx, fallbackQ)
			if err != nil {
				t.Fatal(err)
			}
			defer fallback.Close()
			fallbackExec := fixture.HeldStatement{Executor: conn, Statement: fallback, SQL: fallbackQ}
			fixture.RunStep(t, p.Ctx, fallbackExec, backend.Recorder, fixture.Select(fallbackQ, []string{"v"}, []string{"TEXT"}, [][]any{{"red"}}, "red"))
			run(fixture.Exec("CREATE FUNCTION cast_label(cast_src) RETURNS text LANGUAGE SQL AS $$ SELECT 'bound'::text $$", 0))
			run(fixture.Exec("CREATE CAST (cast_src AS text) WITH FUNCTION cast_label(cast_src)", 0))
			// No pg_cast dependency invalidates PostgreSQL's retained IO node.
			label := "bound"
			if backend.PG {
				label = "red"
			}
			fixture.RunStep(t, p.Ctx, fallbackExec, backend.Recorder, fixture.Select(fallbackQ, []string{"v"}, []string{"TEXT"}, [][]any{{label}}, "red"))
			run(fixture.Select(fallbackQ, []string{"v"}, []string{"TEXT"}, [][]any{{"bound"}}, "red"))
		})
	}
}

func TestReferenceCastDefinitionStoredSemantics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			for _, q := range []string{castDefinitionSource, castDefinitionScore, castDefinitionEdge,
				"CREATE VIEW cast_view AS SELECT ('red'::cast_src)::integer AS v",
				"CREATE TABLE stored_dest(v integer DEFAULT (('blue'::cast_src)::integer))",
				"CREATE FUNCTION cast_stored(cast_src) RETURNS integer LANGUAGE SQL RETURN $1::integer",
				"DROP CAST (cast_src AS integer)",
				"CREATE FUNCTION cast_new(cast_src) RETURNS integer LANGUAGE SQL AS $$ SELECT 77 $$",
				"CREATE CAST (cast_src AS integer) WITH FUNCTION cast_new(cast_src)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Select("SELECT v FROM cast_view", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
			run(fixture.Exec("INSERT INTO stored_dest DEFAULT VALUES", 1))
			run(fixture.Select("SELECT v FROM stored_dest", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			run(fixture.Select("SELECT cast_stored('red'::cast_src) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
			run(fixture.Select("SELECT ('red'::cast_src)::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
			run(fixture.Exec("DROP CAST (cast_src AS integer)", 0))
			run(fixture.Select("SELECT v FROM cast_view", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
			// Frozen selected routines, not the removed pg_cast row, own dependencies.
			s := fixture.ErrorDiagnostic("DROP FUNCTION cast_score(cast_src) RESTRICT", fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function cast_score(cast_src) because other objects depend on it"))
			s.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			s.Diagnostic.Detail = "view cast_view depends on function cast_score(cast_src)\ndefault value for column v of table stored_dest depends on function cast_score(cast_src)\nfunction cast_stored(cast_src) depends on function cast_score(cast_src)"
			run(s)
		})
	}
}

func TestReferenceCastDefinitionDependencies(t *testing.T) {
	for _, endpoint := range []string{"function", "source", "target", "adapter"} {
		t.Run(endpoint, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				if endpoint == "adapter" {
					// REL_18_6 protects all int2/int4/int8 -> bytea pairs.
					// xid -> bytea is absent and still exercises the result adapter.
					castDefinitionCleanup(t, p, backend, [2]string{"text", "bytea"}, [2]string{"xid", "bytea"})
					for _, q := range []string{"CREATE CAST (text AS bytea) WITHOUT FUNCTION AS IMPLICIT", "CREATE FUNCTION cast_bytes(xid) RETURNS text LANGUAGE SQL AS $$ SELECT 'abc'::text $$", "CREATE CAST (xid AS bytea) WITH FUNCTION cast_bytes(xid)"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Select("SELECT ('1'::xid)::bytea AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{"abc"}}))
					s := fixture.ErrorDiagnostic("DROP CAST (text AS bytea) RESTRICT", fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop cast from text to bytea because other objects depend on it"))
					s.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
					s.Diagnostic.Detail = "cast from xid to bytea depends on cast from text to bytea"
					run(s)
					run(fixture.Exec("DROP CAST (text AS bytea) CASCADE", 0))
					run(fixture.Select("SELECT cast_bytes('1'::xid) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"abc"}}))
					run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='xid'::regtype AND casttarget='bytea'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					continue
				}
				setup := []string{castDefinitionSource, "CREATE TYPE cast_other AS ENUM ('red')", castDefinitionScore, castDefinitionEdge}
				if endpoint != "function" {
					// Endpoint DROP uses supported composite types; INOUT owns the
					// dependency without requiring composite SQL routine support.
					setup = []string{"CREATE TYPE cast_src AS (v text)", "CREATE TYPE cast_other AS (v text)", "CREATE CAST (cast_src AS integer) WITH INOUT"}
				}
				setup = append(setup, "CREATE CAST (cast_other AS integer) WITH INOUT", "CREATE FUNCTION cast_score(integer) RETURNS integer LANGUAGE SQL AS $$ SELECT $1 $$", "CREATE TABLE unrelated(v integer)", "INSERT INTO unrelated VALUES (8)")
				for _, q := range setup {
					n := int64(0)
					if q == "INSERT INTO unrelated VALUES (8)" {
						n = 1
					}
					run(fixture.Exec(q, n))
				}
				run(fixture.Exec("CREATE CAST (cast_src AS cast_other) WITH INOUT", 0))
				object, label := "FUNCTION cast_score(cast_src)", "function cast_score(cast_src)"
				if endpoint == "source" {
					object, label = "TYPE cast_src", "type cast_src"
				}
				if endpoint == "target" {
					object, label = "TYPE cast_other", "type cast_other"
				}
				// Check the normal catalog edge independently, not via closure output.
				if endpoint == "function" {
					run(fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_cast c ON d.classid='pg_cast'::regclass AND d.objid=c.oid WHERE c.castsource='cast_src'::regtype AND d.refclassid='pg_proc'::regclass AND d.refobjid='cast_score(cast_src)'::regprocedure AND d.deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				} else {
					typeName := "cast_src"
					if endpoint == "target" {
						typeName = "cast_other"
					}
					run(fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_cast c ON d.classid='pg_cast'::regclass AND d.objid=c.oid WHERE c.castsource='cast_src'::regtype AND c.casttarget='cast_other'::regtype AND d.refclassid='pg_type'::regclass AND d.refobjid='"+typeName+"'::regtype AND d.deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				}
				s := fixture.ErrorDiagnostic("DROP "+object+" RESTRICT", fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop "+label+" because other objects depend on it"))
				s.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				switch endpoint {
				case "function":
					s.Diagnostic.Detail = "cast from cast_src to integer depends on function cast_score(cast_src)"
				case "source":
					s.Diagnostic.Detail = "cast from cast_src to integer depends on type cast_src\ncast from cast_src to cast_other depends on type cast_src"
				case "target":
					s.Diagnostic.Detail = "cast from cast_other to integer depends on type cast_other\ncast from cast_src to cast_other depends on type cast_other"
				}
				run(s)
				run(fixture.Select("SELECT count(*) AS n FROM pg_cast c JOIN pg_type t ON c.castsource=t.oid WHERE t.typname IN ('cast_src','cast_other')", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
				run(fixture.Exec("DROP "+object+" CASCADE", 0))
				if endpoint == "function" {
					run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_src'::regtype AND casttarget='integer'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Select("SELECT count(*) AS n FROM pg_cast c JOIN pg_type t ON c.castsource=t.oid WHERE t.typname IN ('cast_src','cast_other')", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
				}
				if endpoint != "function" {
					survivor := "cast_other"
					if endpoint == "target" {
						survivor = "cast_src"
					}
					run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='"+survivor+"'::regtype AND casttarget='integer'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					run(fixture.Select("SELECT count(*) AS n FROM pg_cast c JOIN pg_type t ON c.castsource=t.oid WHERE t.typname IN ('cast_src','cast_other')", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				}
				run(fixture.Select("SELECT cast_score(9) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
				run(fixture.Select("SELECT v FROM unrelated", []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}))
			}
		})
	}
}
