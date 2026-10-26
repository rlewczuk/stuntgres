package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoutineProceduresOUTArguments(t *testing.T) {
	proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsExec(t, p, b, c,
			"CREATE PROCEDURE pc_out(OUT answer integer) LANGUAGE SQL AS 'SELECT 7'",
			"CREATE SEQUENCE pc_ticks",
			"CREATE PROCEDURE pc_out_big(OUT answer bigint) LANGUAGE SQL AS 'SELECT 7::bigint'",
			"CREATE FUNCTION pc_function(IN x integer,OUT answer integer) LANGUAGE SQL AS 'SELECT x+1'",
			"CREATE PROCEDURE pc_numbered(OUT y integer,IN x integer) LANGUAGE SQL AS 'SELECT $1+1'")
		for _, q := range []string{"CALL pc_out(NULL)", "CALL pc_out(1/0)", "CALL pc_out(answer=>1/0)"} {
			proceduresBoth(t, p, b, c, q, []string{"answer"}, []string{"INT4"}, [][]any{{int64(7)}})
		}
		proceduresBoth(t, p, b, c, "CALL pc_numbered(NULL,4)", []string{"y"}, []string{"INT4"}, [][]any{{int64(5)}})
		proceduresBoth(t, p, b, c, "CALL pc_out_big(nextval('pc_ticks'))", []string{"answer"}, []string{"INT8"}, [][]any{{int64(7)}})
		resultsBothMetadata(t, p, b, c, "SELECT is_called FROM pc_ticks", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}, []resultsNativeColumn{resultsNonNullMetadata})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('pc_ticks') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		resultsBoth(t, p, b, c, "SELECT pc_function(4) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}})
		for _, tc := range []struct {
			q    string
			want fixture.Diagnostic
		}{
			{"CALL pc_out()", proceduresMissing("pc_out()")},
			{"CALL pc_out(1./0.)", proceduresMissing("pc_out(numeric)")},
			{"CALL pc_out(NULL::text)", proceduresMissing("pc_out(text)")},
			{"CALL pc_out('bad')", proceduresDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, 13)},
			{"CALL pc_out(no_such_column)", proceduresDiagnostic("42703", `column "no_such_column" does not exist`, 13)},
		} {
			resultsError(t, p, c, tc.q, true, "prepare", tc.want)
		}
		d := proceduresDiagnostic("42883", "function pc_function(integer, unknown) does not exist", 8)
		d.Hint = resultsNoMatchHint
		resultsError(t, p, c, "SELECT pc_function(4,NULL)", true, "prepare", d)
		// OUT-only protocol parameters are inferred even though the body ignores them.
		q := "CALL pc_out($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, arg := range []any{nil, 123, nil} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, proceduresStep(b, q, []string{"answer"}, []string{"INT4"}, [][]any{{int64(7)}}, arg))
		}
		want := proceduresDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, 0)
		want.Context = "unnamed portal parameter $1 = '...'"
		// Bind conversion, not evaluation of the ignored output expression.
		resultsError(t, p, c, q, true, "execute", want, "bad")
		rows, err := stmt.QueryContext(p.Ctx, "bad")
		if rows != nil {
			rows.Close()
			t.Fatal("bad OUT Bind published rows")
		}
		fixture.AssertDiagnostic(t, err, &want)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, proceduresStep(b, q, []string{"answer"}, []string{"INT4"}, [][]any{{int64(7)}}, nil))
	})
	t.Run("effectful_actuals_and_defaults", func(t *testing.T) {
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c,
				"CREATE TABLE pc_eval(tag text)",
				"CREATE FUNCTION pc_mark(tag text,n integer) RETURNS integer LANGUAGE SQL AS $$INSERT INTO pc_eval VALUES(tag);SELECT n$$",
				"CREATE PROCEDURE pc_eval_call(OUT ignored integer,IN x integer,INOUT y integer,IN z integer DEFAULT pc_mark('default',7)) LANGUAGE SQL AS 'SELECT x+z,y+x'",
				"CREATE PROCEDURE pc_eval_loser(OUT ignored integer,x integer,z integer DEFAULT pc_mark('loser',7)) LANGUAGE SQL AS 'SELECT x'",
				"CREATE PROCEDURE pc_eval_loser(OUT ignored integer,x integer) LANGUAGE SQL AS 'SELECT x'")
			q := "CALL pc_eval_call(pc_mark('out',99),pc_mark('input',$1::integer),pc_mark('inout',$2::integer))"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			proceduresCount(t, p, b, c, "pc_eval", 0)
			for _, route := range []string{"direct", "fresh", "held"} {
				s := proceduresStep(b, q, []string{"ignored", "y"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(14)}}, 4, 10)
				if route == "held" {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
				} else {
					if route == "fresh" {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT tag,count(*) AS n FROM pc_eval GROUP BY tag ORDER BY tag", []string{"tag", "n"}, []string{"TEXT", "INT8"}, [][]any{{"default", int64(3)}, {"inout", int64(3)}, {"input", int64(3)}}))
			ambiguous := proceduresDiagnostic("42725", "procedure pc_eval_loser(unknown, integer) is not unique", 6)
			ambiguous.Hint = "Could not choose a best candidate procedure. You might need to add explicit type casts."
			resultsError(t, p, c, "CALL pc_eval_loser(NULL,4)", false, "prepare", ambiguous)
			resultsError(t, p, c, "CALL pc_eval_loser(NULL,4)", true, "prepare", ambiguous)
			proceduresCount(t, p, b, c, "pc_eval WHERE tag IN ('out','loser')", 0)
			proceduresBoth(t, p, b, c, "CALL pc_eval_call(NULL,4,10,3)", []string{"ignored", "y"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(14)}})
			proceduresCount(t, p, b, c, "pc_eval", 9)
		})
	})
}

func TestReferenceRoutineProceduresNotationDefaults(t *testing.T) {
	proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		proceduresMixSetup(t, p, b, c)
		proceduresMixCheck(t, p, b, c)
		names, types := []string{"first", "total", "note"}, []string{"INT4", "INT8", "TEXT"}
		for _, tc := range []struct {
			q     string
			first int64
		}{
			{"CALL pc_mix(NULL,4,10,NULL,3)", 7},
			{"CALL pc_mix(note=>NULL,total=>10,x=>4,first=>1/0)", 11},
			{"CALL pc_mix(NULL,4,note=>NULL,total=>10,bonus=>3)", 7},
			{"CALL pc_mix(first:=NULL,x:=4,total:=10,note:=NULL)", 11},
		} {
			proceduresBoth(t, p, b, c, tc.q, names, types, [][]any{{tc.first, int64(14), "done"}})
		}
		resultsExec(t, p, b, c,
			"CREATE PROCEDURE pc_default(OUT answer integer,IN x integer DEFAULT 4,INOUT y integer DEFAULT 10) LANGUAGE SQL AS 'SELECT x+1,y+x'",
			`CREATE PROCEDURE pc_quoted(IN "X" integer,OUT "Answer" integer) LANGUAGE SQL AS 'SELECT $1+1'`)
		proceduresBoth(t, p, b, c, "CALL pc_default(NULL)", []string{"answer", "y"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(14)}})
		proceduresBoth(t, p, b, c, "CALL pc_default(answer=>NULL,y=>20)", []string{"answer", "y"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(24)}})
		proceduresBoth(t, p, b, c, `CALL pc_quoted("Answer"=>NULL,"X"=>4)`, []string{"Answer"}, []string{"INT4"}, [][]any{{int64(5)}})
		resultsError(t, p, c, "CALL pc_default()", true, "prepare", proceduresMissing("pc_default()"))
		resultsError(t, p, c, `CALL pc_quoted(answer=>NULL,x=>4)`, true, "prepare", proceduresMissing("pc_quoted(answer => unknown, x => integer)"))
	})
	t.Run("shared_resolution", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "pc_early", "pc_late")
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c,
				"CREATE SCHEMA pc_early", "CREATE SCHEMA pc_late",
				"CREATE PROCEDURE pc_early.pick(OUT answer text,a integer) LANGUAGE SQL AS $$SELECT 'early'::text$$",
				"CREATE PROCEDURE pc_late.pick(OUT answer text,b integer) LANGUAGE SQL AS $$SELECT 'late'::text$$",
				"CREATE PROCEDURE pc_late.rank(OUT answer text,x integer) LANGUAGE SQL AS $$SELECT 'integer'::text$$",
				"CREATE PROCEDURE pc_late.rank(OUT answer text,x text) LANGUAGE SQL AS $$SELECT 'text'::text$$",
				"CREATE PROCEDURE pc_late.map(OUT answer integer,x integer,y bigint) LANGUAGE SQL AS 'SELECT x-y::integer'",
				"CREATE PROCEDURE pc_late.map(OUT answer integer,y bigint,x bigint) LANGUAGE SQL AS 'SELECT (y-x)::integer'",
				"SET search_path=pc_early,pc_late,public")
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "RESET search_path"); err != nil {
					t.Error(err)
				}
			}()
			proceduresBoth(t, p, b, c, "CALL pick(answer=>NULL,b=>4)", []string{"answer"}, []string{"TEXT"}, [][]any{{"late"}})
			proceduresBoth(t, p, b, c, "CALL pick(NULL,4)", []string{"answer"}, []string{"TEXT"}, [][]any{{"early"}})
			proceduresBoth(t, p, b, c, "CALL rank(NULL,'4')", []string{"answer"}, []string{"TEXT"}, [][]any{{"text"}})
			proceduresBoth(t, p, b, c, "CALL rank(NULL,4)", []string{"answer"}, []string{"TEXT"}, [][]any{{"integer"}})
			proceduresBoth(t, p, b, c, "CALL map(y=>2::bigint,answer=>NULL,x=>9::integer)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(7)}})
			proceduresBoth(t, p, b, c, "CALL map(x=>2::bigint,answer=>NULL,y=>9::integer)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(7)}})
		})
	})
}

func TestReferenceRoutineProceduresVariadic(t *testing.T) {
	proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsExec(t, p, b, c,
			"CREATE PROCEDURE pc_array(OUT answer integer[],VARIADIC xs integer[]) LANGUAGE SQL AS 'SELECT xs'",
			"CREATE PROCEDURE pc_array_default(OUT answer integer[],VARIADIC xs integer[] DEFAULT '{}'::integer[]) LANGUAGE SQL AS 'SELECT xs'",
			"CREATE PROCEDURE pc_compete(OUT answer text,VARIADIC xs integer[]) LANGUAGE SQL AS $$SELECT 'variadic'::text$$",
			"CREATE PROCEDURE pc_compete(OUT answer text,x integer) LANGUAGE SQL AS $$SELECT 'exact'::text$$")
		resultsExec(t, p, b, c, "CREATE PROCEDURE pc_nonvariadic_array(INOUT xs integer[]) LANGUAGE SQL AS 'SELECT xs'")
		proceduresBoth(t, p, b, c, "CALL pc_nonvariadic_array(VARIADIC ARRAY[1,2])", []string{"xs"}, []string{"_INT4"}, [][]any{{"{1,2}"}})
		for _, tc := range []struct {
			q string
			v any
		}{
			{"CALL pc_array(NULL,1,NULL,3)", "{1,NULL,3}"},
			{"CALL pc_array(NULL,VARIADIC ARRAY[4,6])", "{4,6}"},
			{"CALL pc_array(answer=>NULL,VARIADIC xs=>ARRAY[4,6])", "{4,6}"},
			{"CALL pc_array(NULL,VARIADIC ARRAY[]::integer[])", "{}"},
			{"CALL pc_array(NULL,VARIADIC NULL::integer[])", nil},
			{"CALL pc_array(NULL,VARIADIC '[0:1]={8,9}'::integer[])", "[0:1]={8,9}"},
			{"CALL pc_array(NULL,VARIADIC '[0:1][2:3]={{1,NULL},{3,4}}'::integer[])", "[0:1][2:3]={{1,NULL},{3,4}}"},
			{"CALL pc_array_default(NULL)", "{}"},
		} {
			proceduresBoth(t, p, b, c, tc.q, []string{"answer"}, []string{"_INT4"}, [][]any{{tc.v}})
		}
		proceduresHeld(t, p, b, c, "CALL pc_array(NULL,$1::integer,$2::integer)", []string{"answer"}, []string{"_INT4"}, [][]any{{4, 6}, {nil, 8}}, [][][]any{{{"{4,6}"}}, {{"{NULL,8}"}}})
		proceduresHeld(t, p, b, c, "CALL pc_array(NULL,VARIADIC $1::integer[])", []string{"answer"}, []string{"_INT4"}, [][]any{{"[0:1]={8,9}"}, {nil}, {"{}"}}, [][][]any{{{"[0:1]={8,9}"}}, {{nil}}, {{"{}"}}})
		proceduresBoth(t, p, b, c, "CALL pc_compete(NULL,4)", []string{"answer"}, []string{"TEXT"}, [][]any{{"exact"}})
		proceduresBoth(t, p, b, c, "CALL pc_compete(NULL,VARIADIC ARRAY[4])", []string{"answer"}, []string{"TEXT"}, [][]any{{"variadic"}})
		for _, tc := range []struct{ q, sig string }{
			{"CALL pc_array(NULL)", "pc_array(unknown)"},
			{"CALL pc_array(NULL,ARRAY[4,6])", "pc_array(unknown, integer[])"},
			{"CALL pc_array(answer=>NULL,xs=>1)", "pc_array(answer => unknown, xs => integer)"},
		} {
			resultsError(t, p, c, tc.q, true, "prepare", proceduresMissing(tc.sig))
		}
	})
}
