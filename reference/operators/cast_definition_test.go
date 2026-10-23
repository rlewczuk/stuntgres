package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// PostgreSQL REL_18_6 functioncmds.c:CreateCast and parse_coerce.c are the
// independent authority for these candidates. Empty diagnostic fields are
// assertions, not wildcards: oracle discovery must report every unknown field.
const castDefinitionSource = "CREATE TYPE cast_src AS ENUM ('red','blue')"
const castDefinitionScore = `CREATE FUNCTION cast_score(cast_src) RETURNS integer LANGUAGE SQL AS $$ SELECT CASE WHEN $1 IS NULL THEN 99 WHEN $1::text='red' THEN 41 ELSE 42 END $$`
const castDefinitionEdge = "CREATE CAST (cast_src AS integer) WITH FUNCTION cast_score(cast_src)"

// Builtin endpoints outlive public-schema reset. Register before CREATE, check
// ownership by absence first, and use a live cleanup context even after failure.
func castDefinitionCleanup(t *testing.T, p *fixture.Pair, backend fixture.Backend, pairs ...[2]string) {
	t.Helper()
	for _, pair := range pairs {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select(
			"SELECT count(*) AS n FROM pg_cast WHERE castsource=$1::regtype AND casttarget=$2::regtype",
			[]string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, pair[0], pair[1]))
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := backend.DB.ExecContext(ctx, "DROP CAST IF EXISTS ("+pair[0]+" AS "+pair[1]+") CASCADE"); err != nil {
				t.Errorf("cast cleanup %s to %s: %v", pair[0], pair[1], err)
			}
		})
	}
}

func TestReferenceCastDefinitionFunctionAndContexts(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			for _, q := range []string{castDefinitionSource, castDefinitionScore,
				"CREATE FUNCTION cast_receiver(integer) RETURNS integer LANGUAGE SQL AS $$ SELECT $1+100 $$",
				"CREATE TABLE cast_dest(n integer)", castDefinitionEdge} {
				run(fixture.Exec(q, 0))
			}
			for _, prepared := range []bool{false, true} {
				for _, v := range []struct {
					input  any
					answer any
				}{{"red", int64(41)}, {"blue", int64(42)}, {nil, int64(99)}} {
					s := fixture.Select("SELECT ($1::cast_src)::integer AS answer", []string{"answer"}, []string{"INT4"}, [][]any{{v.answer}}, v.input)
					s.Prepared = prepared
					run(s)
				}
			}
			for _, mode := range []string{"", " AS ASSIGNMENT", " AS IMPLICIT"} {
				if mode != "" {
					run(fixture.Exec("DROP CAST (cast_src AS integer) RESTRICT", 0))
					run(fixture.Prepared(fixture.Exec(castDefinitionEdge+mode, 0)))
				}
				if mode == "" {
					s := fixture.ErrorDiagnostic("INSERT INTO cast_dest VALUES ('red'::cast_src)", fixture.ControlsReferenceDiagnostic("42804", `column "n" is of type integer but expression is of type cast_src`))
					s.Diagnostic.Hint = "You will need to rewrite or cast the expression."
					s.Diagnostic.Position = "31"
					s.ErrorStage = "execute"
					run(s)
				} else {
					run(fixture.Exec("INSERT INTO cast_dest VALUES ('red'::cast_src)", 1))
				}
				if mode == " AS IMPLICIT" {
					run(fixture.Select("SELECT cast_receiver('red'::cast_src) AS answer", []string{"answer"}, []string{"INT4"}, [][]any{{int64(141)}}))
				} else {
					s := fixture.ErrorDiagnostic("SELECT cast_receiver('red'::cast_src)", fixture.ControlsReferenceDiagnostic("42883", "function cast_receiver(cast_src) does not exist"))
					s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					s.Diagnostic.Position = "8"
					run(s)
				}
				context := "e"
				if mode == " AS ASSIGNMENT" {
					context = "a"
				}
				if mode == " AS IMPLICIT" {
					context = "i"
				}
				run(fixture.Select(`SELECT c.castcontext::text AS context, c.castmethod::text AS method, c.castfunc = 'cast_score(cast_src)'::regprocedure::oid AS routine FROM pg_cast c WHERE c.castsource='cast_src'::regtype AND c.casttarget='integer'::regtype`, []string{"context", "method", "routine"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{context, "f", true}}))
			}
			run(fixture.Select("SELECT n FROM cast_dest ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(41)}, {int64(41)}}))
			run(fixture.Exec("DROP CAST (cast_src AS integer) CASCADE", 0))
			d := fixture.ControlsReferenceDiagnostic("42846", "cannot cast type cast_src to integer")
			d.Position = "25"
			run(fixture.ErrorDiagnostic("SELECT ('red'::cast_src)::integer", d))
			run(fixture.Select("SELECT cast_score('blue'::cast_src) AS answer", []string{"answer"}, []string{"INT4"}, [][]any{{int64(42)}}))
		})
	}
}

func TestReferenceCastDefinitionSignatures(t *testing.T) {
	// Each rejection has its own fresh fixture so oracle failures do not hide
	// subsequent candidates. Signature validation precedes duplicate detection.
	for _, tc := range []struct{ name, declaration, signature, message string }{
		{"zero", "CREATE FUNCTION bad() RETURNS integer LANGUAGE SQL AS $$ SELECT 1 $$", "bad()", "cast function must take one to three arguments"},
		{"four", "CREATE FUNCTION bad(cast_src,integer,boolean,integer) RETURNS integer LANGUAGE SQL AS $$ SELECT 1 $$", "bad(cast_src,integer,boolean,integer)", "cast function must take one to three arguments"},
		{"first", "CREATE FUNCTION bad(text) RETURNS integer LANGUAGE SQL AS $$ SELECT 1 $$", "bad(text)", "argument of cast function must match or be binary-coercible from source data type"},
		{"second", "CREATE FUNCTION bad(cast_src,text) RETURNS integer LANGUAGE SQL AS $$ SELECT 1 $$", "bad(cast_src,text)", "second argument of cast function must be type integer"},
		{"third", "CREATE FUNCTION bad(cast_src,integer,integer) RETURNS integer LANGUAGE SQL AS $$ SELECT 1 $$", "bad(cast_src,integer,integer)", "third argument of cast function must be type boolean"},
		{"result", "CREATE FUNCTION bad(cast_src) RETURNS text LANGUAGE SQL AS $$ SELECT 'bad'::text $$", "bad(cast_src)", "return data type of cast function must match or be binary-coercible to target data type"},
		{"aggregate_first_validation", "", "sum(integer)", "argument of cast function must match or be binary-coercible from source data type"},
		{"window", "", "row_number()", "cast function must take one to three arguments"},
		{"set", "", "generate_series(integer,integer)", "argument of cast function must match or be binary-coercible from source data type"},
		{"procedure", "CREATE PROCEDURE bad(cast_src) LANGUAGE SQL AS $$ SELECT 1 $$", "bad(cast_src)", "bad(cast_src) is not a function"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				run(fixture.Exec(castDefinitionSource, 0))
				if tc.declaration != "" {
					run(fixture.Exec(tc.declaration, 0))
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("CREATE CAST (cast_src AS integer) WITH FUNCTION "+tc.signature, fixture.ControlsReferenceDiagnostic("42P17", tc.message))
					if tc.name == "procedure" {
						s.State = "42809"
						s.Diagnostic.Code = "42809"
					}
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
				}
				run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_src'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
		})
	}
	for _, tc := range []struct{ name, setup, query, state, message string }{
		{"omitted_ambiguous", "CREATE FUNCTION cast_score(integer) RETURNS integer LANGUAGE SQL AS $$ SELECT $1 $$", "CREATE CAST (cast_src AS integer) WITH FUNCTION cast_score", "42725", "function name \"cast_score\" is not unique"},
		{"exact_overload", "CREATE FUNCTION cast_score(integer) RETURNS integer LANGUAGE SQL AS $$ SELECT $1 $$", castDefinitionEdge, "", ""},
		{"bare_unique", "", "CREATE CAST (cast_src AS integer) WITH FUNCTION cast_score", "", ""},
		{"duplicate", castDefinitionEdge, castDefinitionEdge, "42710", "cast from type cast_src to type integer already exists"},
		{"pseudo_source", "", "CREATE CAST (void AS integer) WITH INOUT", "42809", "source data type void is a pseudo-type"},
		{"pseudo_target", "", "CREATE CAST (cast_src AS void) WITH INOUT", "42809", "target data type void is a pseudo-type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				run(fixture.Exec(castDefinitionSource, 0))
				run(fixture.Exec(castDefinitionScore, 0))
				if tc.setup != "" {
					run(fixture.Exec(tc.setup, 0))
				}
				if tc.state == "" {
					run(fixture.Exec(tc.query, 0))
					run(fixture.Select("SELECT ('red'::cast_src)::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
				} else {
					s := fixture.ErrorDiagnostic(tc.query, fixture.ControlsReferenceDiagnostic(tc.state, tc.message))
					if tc.name == "omitted_ambiguous" {
						s.Diagnostic.Hint = "Specify the argument list to select the function unambiguously."
					}
					run(s)
				}
			}
		})
	}
}

func TestReferenceCastDefinitionIO(t *testing.T) {
	for _, mode := range []string{"", " AS ASSIGNMENT", " AS IMPLICIT"} {
		t.Run("context"+mode, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				conn, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
				for _, q := range []string{castDefinitionSource, "CREATE TYPE cast_other AS ENUM ('red')", "CREATE TABLE io_dest(v cast_other)", "CREATE FUNCTION io_receive(cast_other) RETURNS integer LANGUAGE SQL AS $$ SELECT 7 $$"} {
					run(fixture.Exec(q, 0))
				}
				d := fixture.ControlsReferenceDiagnostic("42846", "cannot cast type cast_src to cast_other")
				d.Position = "25"
				run(fixture.ErrorDiagnostic("SELECT ('red'::cast_src)::cast_other", d))
				run(fixture.Exec("CREATE CAST (cast_src AS cast_other) WITH INOUT"+mode, 0))
				for _, prepared := range []bool{false, true} {
					for _, input := range []any{"red", nil} {
						s := fixture.Select("SELECT ($1::cast_src)::cast_other AS v", []string{"v"}, []string{""}, [][]any{{input}}, input)
						s.Prepared = prepared
						s.CheckMetadata = fixture.EnumReferenceMetadata(backend.PG, false)
						fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, s)
					}
					s := fixture.ErrorDiagnostic("SELECT ($1::cast_src)::cast_other", fixture.ControlsReferenceDiagnostic("22P02", `invalid input value for enum cast_other: "blue"`), "blue")
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
				}
				if mode != "" {
					run(fixture.Exec("INSERT INTO io_dest VALUES ('red'::cast_src)", 1))
				}
				if mode == " AS IMPLICIT" {
					run(fixture.Select("SELECT io_receive('red'::cast_src) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
				run(fixture.Select("SELECT castfunc=0 AS no_routine,castmethod::text AS method FROM pg_cast WHERE castsource='cast_src'::regtype AND casttarget='cast_other'::regtype", []string{"no_routine", "method"}, []string{"BOOL", "TEXT"}, [][]any{{true, "i"}}))
				run(fixture.Exec("DROP CAST (cast_src AS cast_other)", 0))
				run(fixture.ErrorDiagnostic("SELECT ('red'::cast_src)::cast_other", d))
			}
		})
	}
}

func TestReferenceCastDefinitionFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			castDefinitionCleanup(t, p, backend, [2]string{"real", "xid"}, [2]string{"xid", "real"}, [2]string{"text", "bytea"})
			for _, q := range []string{castDefinitionSource, castDefinitionScore, castDefinitionEdge,
				`CREATE FUNCTION cast_mark(cast_src,integer,boolean) RETURNS varchar LANGUAGE SQL AS $$ SELECT CASE WHEN $3 THEN 'E:' ELSE 'A:' END || $2::text $$`,
				"CREATE CAST (cast_src AS varchar) WITH FUNCTION cast_mark(cast_src,integer,boolean) AS ASSIGNMENT",
				"CREATE TABLE cast_marks(v varchar(8))", "CREATE CAST (xid AS real) WITHOUT FUNCTION"} {
				run(fixture.Exec(q, 0))
			}
			q := "SELECT ($1::cast_src)::integer AS answer"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				in  any
				out int64
			}{{"red", 41}, {"blue", 42}, {nil, 99}, {"red", 41}} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, []string{"answer"}, []string{"INT4"}, [][]any{{tc.out}}, tc.in))
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT ('red'::cast_src)::varchar AS plain, ('red'::cast_src)::varchar(5) AS sized", []string{"plain", "sized"}, []string{"VARCHAR", "VARCHAR"}, [][]any{{"E:-1", "E:9"}})
				s.Prepared = prepared
				run(s)
				s = fixture.Select("SELECT ($1::xid)::real AS v", []string{"v"}, []string{"FLOAT4"}, [][]any{{float64(1)}}, "1065353216")
				s.Prepared = prepared
				run(s)
			}
			run(fixture.Exec("INSERT INTO cast_marks VALUES ('red'::cast_src)", 1))
			run(fixture.Select("SELECT v FROM cast_marks", []string{"v"}, []string{"VARCHAR"}, [][]any{{"A:12"}}))
			for _, q := range []string{"CREATE TYPE cast_other AS ENUM ('red')", "CREATE CAST (cast_src AS cast_other) WITH INOUT AS ASSIGNMENT", "CREATE TABLE io_dest(v cast_other)", "CREATE CAST (real AS xid) WITHOUT FUNCTION", "CREATE CAST (text AS bytea) WITHOUT FUNCTION"} {
				run(fixture.Exec(q, 0))
			}
			s := fixture.Select("SELECT ('red'::cast_src)::cast_other AS v", []string{"v"}, []string{""}, [][]any{{"red"}})
			s.CheckMetadata = fixture.EnumReferenceMetadata(backend.PG, false)
			fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, s)
			s = fixture.ErrorDiagnostic("INSERT INTO io_dest VALUES ('red'::cast_src),('blue'::cast_src)", fixture.ControlsReferenceDiagnostic("22P02", `invalid input value for enum cast_other: "blue"`))
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Select("SELECT count(*) AS n FROM io_dest", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			for _, word := range []string{"2143289345", "2139095041"} {
				run(fixture.Select("SELECT (($1::xid)::real)::xid::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{word}}, word))
			}
			run(fixture.Select("SELECT ($1::text)::bytea AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{`\x4142`}}, `\x4142`))
			run(fixture.Exec("DROP CAST (cast_src AS varchar)", 0))
			run(fixture.Exec("CREATE CAST (cast_src AS varchar) WITH FUNCTION cast_mark(cast_src,integer,boolean) AS IMPLICIT", 0))
			run(fixture.Exec("CREATE FUNCTION cast_varchar_receiver(varchar) RETURNS text LANGUAGE SQL AS $$ SELECT $1::text $$", 0))
			run(fixture.Select("SELECT cast_varchar_receiver('red'::cast_src) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"A:-1"}}))
		})
	}
}
