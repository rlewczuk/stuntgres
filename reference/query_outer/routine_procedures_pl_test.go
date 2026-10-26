package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoutineProceduresPL(t *testing.T) {
	proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsExec(t, p, b, c,
			"CREATE TABLE pc_pl_log(v integer)",
			"CREATE PROCEDURE pc_pl(IN x integer,INOUT y integer,OUT note text) LANGUAGE plpgsql AS $$BEGIN y:=y+x;note:='done';RETURN;END$$",
			"CREATE PROCEDURE pc_pl_fall(IN x integer,INOUT y integer,OUT note text) LANGUAGE plpgsql AS $$BEGIN y:=y+x;note:='done';END$$",
			"CREATE PROCEDURE pc_pl_null(OUT answer integer) LANGUAGE plpgsql AS $$BEGIN RETURN;END$$",
			"CREATE PROCEDURE pc_pl_default(OUT answer integer,INOUT n integer DEFAULT 10) LANGUAGE plpgsql AS $$BEGIN answer:=n+1;n:=n+2;END$$",
			"CREATE PROCEDURE pc_pl_numbered(OUT answer integer,IN x integer) LANGUAGE plpgsql AS $$BEGIN $1:=$2+1;END$$",
			"CREATE PROCEDURE pc_pl_effect(IN x integer,OUT answer integer) LANGUAGE plpgsql AS $$BEGIN INSERT INTO pc_pl_log VALUES(x) RETURNING v INTO answer;answer:=x+1;END$$",
			"CREATE PROCEDURE pc_inner(IN x integer) LANGUAGE SQL AS 'INSERT INTO pc_pl_log VALUES(x)'",
			"CREATE PROCEDURE pc_outer(IN x integer) LANGUAGE SQL AS 'CALL pc_inner(x)'")
		qStandard := "CREATE PROCEDURE pc_outer_standard(IN x integer) BEGIN ATOMIC CALL pc_inner(x);END"
		wantStandard := proceduresDiagnostic("0A000", "CALL is not yet supported in unquoted SQL function body", 0)
		resultsError(t, p, c, qStandard, false, "execute", wantStandard)
		proceduresCount(t, p, b, c, "pg_proc WHERE proname='pc_outer_standard'", 0)
		resultsExec(t, p, b, c, "CREATE PROCEDURE pc_outer_standard(IN x integer) LANGUAGE SQL AS 'CALL pc_inner(x)'")
		for _, name := range []string{"pc_pl", "pc_pl_fall"} {
			proceduresBoth(t, p, b, c, "CALL "+name+"(4,10,NULL)", []string{"y", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(14), "done"}})
			proceduresBoth(t, p, b, c, "CALL "+name+"(note=>NULL,y=>10,x=>4)", []string{"y", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(14), "done"}})
			proceduresHeld(t, p, b, c, "CALL "+name+"($1::integer,$2::integer,NULL)", []string{"y", "note"}, []string{"INT4", "TEXT"}, [][]any{{4, 10}, {8, 20}, {4, nil}}, [][][]any{{{int64(14), "done"}}, {{int64(28), "done"}}, {{nil, "done"}}})
		}
		proceduresBoth(t, p, b, c, "CALL pc_pl_null(NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{nil}})
		proceduresBoth(t, p, b, c, "CALL pc_pl_default(NULL)", []string{"answer", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(12)}})
		proceduresBoth(t, p, b, c, "CALL pc_pl_numbered(NULL,4)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}})
		q := "CALL pc_pl_effect($1::integer,NULL)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		proceduresCount(t, p, b, c, "pc_pl_log", 0)
		proceduresBoth(t, p, b, c, "CALL pc_pl_effect(4,NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, proceduresStep(b, q, []string{"answer"}, []string{"INT4"}, [][]any{{int64(9)}}, 8))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("CALL pc_outer(9)", 0)
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		proceduresBoth(t, p, b, c, "CALL pc_outer_standard(9)", []string{}, []string{}, [][]any{})
		resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_pl_log ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(4)}, {int64(8)}, {int64(9)}, {int64(9)}, {int64(9)}, {int64(9)}})
	})
	t.Run("body_restrictions", func(t *testing.T) {
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c, "CREATE PROCEDURE pc_out(OUT answer integer) LANGUAGE SQL AS 'SELECT 7'")
			q := "CREATE PROCEDURE pc_nested() LANGUAGE SQL AS 'CALL pc_out(NULL)'"
			want := proceduresDiagnostic("0A000", "calling procedures with output arguments is not supported in SQL functions", 0)
			want.Context = `SQL function "pc_nested"`
			resultsError(t, p, c, q, false, "execute", want)
			proceduresCount(t, p, b, c, "pg_proc WHERE proname='pc_nested'", 0)
			resultsExec(t, p, b, c, "CREATE PROCEDURE pc_nested() LANGUAGE SQL AS 'SELECT 1'")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CALL pc_nested()", 0))
			q = "CREATE PROCEDURE pc_bad_number(OUT y integer,IN x integer) LANGUAGE SQL AS 'SELECT $2'"
			want = proceduresDiagnostic("42P02", "there is no parameter $2", strings.Index(q, "$2")+1)
			resultsError(t, p, c, q, false, "execute", want)
			proceduresCount(t, p, b, c, "pg_proc WHERE proname='pc_bad_number'", 0)
			resultsExec(t, p, b, c, "CREATE PROCEDURE pc_bad_number(OUT y integer,IN x integer) LANGUAGE SQL AS 'SELECT $1'")
			proceduresBoth(t, p, b, c, "CALL pc_bad_number(NULL,4)", []string{"y"}, []string{"INT4"}, [][]any{{int64(4)}})
			q = "CREATE PROCEDURE pc_bad_pl(IN a integer,OUT a integer) LANGUAGE plpgsql AS $$BEGIN RETURN;END$$"
			want = proceduresDiagnostic("42P13", `parameter name "a" used more than once`, 0)
			want.Context = "compilation of PL/pgSQL function \"pc_bad_pl\" near line 1"
			resultsError(t, p, c, q, false, "execute", want)
			proceduresCount(t, p, b, c, "pg_proc WHERE proname='pc_bad_pl'", 0)
			resultsExec(t, p, b, c, "CREATE PROCEDURE pc_bad_pl(IN a integer,OUT b integer) LANGUAGE plpgsql AS $$BEGIN b:=a+1;END$$")
			proceduresBoth(t, p, b, c, "CALL pc_bad_pl(4,NULL)", []string{"b"}, []string{"INT4"}, [][]any{{int64(5)}})
		})
	})
}
