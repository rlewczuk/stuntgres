package reference_test

import (
	"database/sql"
	"math"
	"testing"
)

func numericSumPrepared(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
	for _, c := range []struct{ kind, typ string }{{"smallint", "INT8"}, {"integer", "INT8"}, {"bigint", "NUMERIC"}, {"numeric", "NUMERIC"}, {"real", "FLOAT4"}, {"double precision", "FLOAT8"}} {
		q := "SELECT sum($1::" + c.kind + ")"
		stmt, err := conn.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
			for _, v := range []any{int64(1), int64(3), nil, "oops", int64(4)} {
				if v == "oops" {
					s := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type " + c.kind + ": \"oops\"", Context: "unnamed portal parameter $1 = '...'"}, v)
					s.selectRows, s.errorStage = true, "execute"
					referenceRunStep(t, p.ctx, held, recorder, s)
					continue
				}
				var want any = v
				if v != nil {
					n := v.(int64)
					if c.typ == "NUMERIC" {
						want = referencePosition(int(n))
					}
					if c.typ == "FLOAT4" || c.typ == "FLOAT8" {
						want = float64(n)
					}
				}
				s := referenceSelect(q, []string{"sum"}, []string{c.typ}, [][]any{{want}}, v)
				s.checkMetadata = numericSumMetadata(pg, s.types)
				referenceRunStep(t, p.ctx, held, recorder, s)
			}
		}()
	}
}

func TestReferenceNumericSumPrepared(t *testing.T) {
	numericSumScenario(t, numericSumPrepared)
}

func TestReferenceNumericSumLifecycle(t *testing.T) {
	numericSumScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		for _, c := range []struct{ name, kind, typ, bad, message string }{
			{"exact", "NUMERIC", "NUMERIC", "('9e131071'),('9e131071')", "value overflows numeric format"},
			{"floating", "DOUBLE PRECISION", "FLOAT8", "(1.7976931348623157e308),(1.7976931348623157e308)", "value out of range: overflow"},
		} {
			run(referenceExec("CREATE TABLE "+c.name+"(v "+c.kind+")", 0))
			run(referenceExec("INSERT INTO "+c.name+" VALUES (1),(3)", 2))
			q := "SELECT sum(v) FROM " + c.name
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
				check := func(n string, d float64, null bool) {
					var want any = n
					if c.typ == "FLOAT8" {
						want = d
					}
					if null {
						want = nil
					}
					s := referenceSelect(q, []string{"sum"}, []string{c.typ}, [][]any{{want}})
					s.checkMetadata = numericSumMetadata(pg, s.types)
					referenceRunStep(t, p.ctx, held, recorder, s)
				}
				check("4", 4, false)
				run(referenceExec("INSERT INTO "+c.name+" VALUES (8),(NULL)", 2))
				check("12", 12, false)
				run(referenceExec("DELETE FROM "+c.name, 4))
				check("", 0, true)
				run(referenceExec("INSERT INTO "+c.name+" VALUES (NULL)", 1))
				check("", 0, true)
				run(referenceExec("DELETE FROM "+c.name, 1))
				run(referenceExec("INSERT INTO "+c.name+" VALUES "+c.bad, 2))
				s := referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: c.message})
				s.selectRows, s.errorStage = true, "execute"
				referenceRunStep(t, p.ctx, held, recorder, s)
				run(referenceExec("DELETE FROM "+c.name, 2))
				run(referenceExec("INSERT INTO "+c.name+" VALUES (1),(3)", 2))
				check("4", 4, false)
			}()
		}
		run(referenceExec("CREATE TABLE late(id INTEGER,n NUMERIC,v TEXT)", 0))
		for _, special := range []string{"Infinity", "NaN"} {
			run(referenceExec("INSERT INTO late VALUES (1,'"+special+"','1'),(2,0,'oops')", 2))
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("SELECT sum(CASE WHEN v::integer=1 THEN n ELSE NULL END ORDER BY id) FROM late", referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type integer: \"oops\""})
				s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
				run(s)
			}
			run(referenceExec("DELETE FROM late", 2))
		}
	})
}

func TestReferenceNumericSumSmoke(t *testing.T) {
	numericSumScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		numericSumArithmetic(t, p, conn, recorder, pg)
		numericSumPrepared(t, p, conn, recorder, pg)
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE promotion(b BIGINT,n NUMERIC,id INTEGER,r REAL,d DOUBLE PRECISION)", 0))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO promotion VALUES (9223372036854775807,1.20,1,16777216,'-0'),(9223372036854775807,3.456,2,1,'-0'),(NULL,NULL,3,-16777216,NULL)", 3))
		numericSumBoth(t, p, conn, recorder, referenceSelect("SELECT sum(b),sum(n),sum(r ORDER BY id) FROM promotion", []string{"sum", "sum", "sum"}, []string{"NUMERIC", "NUMERIC", "FLOAT4"}, [][]any{{"18446744073709551614", "4.656", float64(0)}}))
		numericSumFloatResult(t, p, conn, pg, "SELECT sum(d) FROM promotion", "FLOAT8", math.Copysign(0, -1), 0)
		intervalAvgDiagnostic(t, p, conn, recorder, "SELECT sum(TRUE) FROM t", referenceDiagnostic{Code: "42883", Message: "function sum(boolean) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
		intervalAvgDiagnostic(t, p, conn, recorder, "SELECT sum(NULL) FROM t", referenceDiagnostic{Code: "42725", Message: "function sum(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: "8"})
		intervalAvgDiagnostic(t, p, conn, recorder, "SELECT i FROM t WHERE sum(i)=1", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in WHERE", Position: "23"})
	})
	t.Run("empty and nullable metadata", TestReferenceNumericSumEmptyAndMetadata)
	t.Run("consumers and atomic failures", TestReferenceNumericSumConsumers)
	t.Run("view restore and actual emitted replay", TestReferenceNumericSumControls)
}
