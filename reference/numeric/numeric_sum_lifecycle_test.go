package reference_test

import (
	"database/sql"
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

func numericSumPrepared(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
	for _, c := range []struct{ kind, typ string }{{"smallint", "INT8"}, {"integer", "INT8"}, {"bigint", "NUMERIC"}, {"numeric", "NUMERIC"}, {"real", "FLOAT4"}, {"double precision", "FLOAT8"}} {
		q := "SELECT sum($1::" + c.kind + ")"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			for _, v := range []any{int64(1), int64(3), nil, "oops", int64(4)} {
				if v == "oops" {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type " + c.kind + ": \"oops\"", Context: "unnamed portal parameter $1 = '...'"}, v)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, recorder, s)
					continue
				}
				var want any = v
				if v != nil {
					n := v.(int64)
					if c.typ == "NUMERIC" {
						want = fixture.Position(int(n))
					}
					if c.typ == "FLOAT4" || c.typ == "FLOAT8" {
						want = float64(n)
					}
				}
				s := fixture.Select(q, []string{"sum"}, []string{c.typ}, [][]any{{want}}, v)
				s.CheckMetadata = numericSumMetadata(pg, s.Types)
				fixture.RunStep(t, p.Ctx, held, recorder, s)
			}
		}()
	}
}

func TestReferenceNumericSumPrepared(t *testing.T) {
	numericSumScenario(t, numericSumPrepared)
}

func TestReferenceNumericSumLifecycle(t *testing.T) {
	numericSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		for _, c := range []struct{ name, kind, typ, bad, message string }{
			{"exact", "NUMERIC", "NUMERIC", "('9e131071'),('9e131071')", "value overflows numeric format"},
			{"floating", "DOUBLE PRECISION", "FLOAT8", "(1.7976931348623157e308),(1.7976931348623157e308)", "value out of range: overflow"},
		} {
			run(fixture.Exec("CREATE TABLE "+c.name+"(v "+c.kind+")", 0))
			run(fixture.Exec("INSERT INTO "+c.name+" VALUES (1),(3)", 2))
			q := "SELECT sum(v) FROM " + c.name
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				check := func(n string, d float64, null bool) {
					var want any = n
					if c.typ == "FLOAT8" {
						want = d
					}
					if null {
						want = nil
					}
					s := fixture.Select(q, []string{"sum"}, []string{c.typ}, [][]any{{want}})
					s.CheckMetadata = numericSumMetadata(pg, s.Types)
					fixture.RunStep(t, p.Ctx, held, recorder, s)
				}
				check("4", 4, false)
				run(fixture.Exec("INSERT INTO "+c.name+" VALUES (8),(NULL)", 2))
				check("12", 12, false)
				run(fixture.Exec("DELETE FROM "+c.name, 4))
				check("", 0, true)
				run(fixture.Exec("INSERT INTO "+c.name+" VALUES (NULL)", 1))
				check("", 0, true)
				run(fixture.Exec("DELETE FROM "+c.name, 1))
				run(fixture.Exec("INSERT INTO "+c.name+" VALUES "+c.bad, 2))
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: c.message})
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, held, recorder, s)
				run(fixture.Exec("DELETE FROM "+c.name, 2))
				run(fixture.Exec("INSERT INTO "+c.name+" VALUES (1),(3)", 2))
				check("4", 4, false)
			}()
		}
		run(fixture.Exec("CREATE TABLE late(id INTEGER,n NUMERIC,v TEXT)", 0))
		for _, special := range []string{"Infinity", "NaN"} {
			run(fixture.Exec("INSERT INTO late VALUES (1,'"+special+"','1'),(2,0,'oops')", 2))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT sum(CASE WHEN v::integer=1 THEN n ELSE NULL END ORDER BY id) FROM late", fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type integer: \"oops\""})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				run(s)
			}
			run(fixture.Exec("DELETE FROM late", 2))
		}
	})
}

func TestReferenceNumericSumSmoke(t *testing.T) {
	numericSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		numericSumArithmetic(t, p, conn, recorder, pg)
		numericSumPrepared(t, p, conn, recorder, pg)
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE promotion(b BIGINT,n NUMERIC,id INTEGER,r REAL,d DOUBLE PRECISION)", 0))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO promotion VALUES (9223372036854775807,1.20,1,16777216,'-0'),(9223372036854775807,3.456,2,1,'-0'),(NULL,NULL,3,-16777216,NULL)", 3))
		numericSumBoth(t, p, conn, recorder, fixture.Select("SELECT sum(b),sum(n),sum(r ORDER BY id) FROM promotion", []string{"sum", "sum", "sum"}, []string{"NUMERIC", "NUMERIC", "FLOAT4"}, [][]any{{"18446744073709551614", "4.656", float64(0)}}))
		numericSumFloatResult(t, p, conn, pg, "SELECT sum(d) FROM promotion", "FLOAT8", math.Copysign(0, -1), 0)
		fixture.IntervalAvgDiagnostic(t, p, conn, recorder, "SELECT sum(TRUE) FROM t", fixture.Diagnostic{Code: "42883", Message: "function sum(boolean) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
		fixture.IntervalAvgDiagnostic(t, p, conn, recorder, "SELECT sum(NULL) FROM t", fixture.Diagnostic{Code: "42725", Message: "function sum(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: "8"})
		fixture.IntervalAvgDiagnostic(t, p, conn, recorder, "SELECT i FROM t WHERE sum(i)=1", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in WHERE", Position: "23"})
	})
	t.Run("empty and nullable metadata", TestReferenceNumericSumEmptyAndMetadata)
	t.Run("consumers and atomic failures", TestReferenceNumericSumConsumers)
	t.Run("view restore and actual emitted replay", TestReferenceNumericSumControls)
}
