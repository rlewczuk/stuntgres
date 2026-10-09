package reference_test

import (
	"database/sql"
	"testing"
)

func numericAvgAtomicity(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
	for _, c := range []struct{ suffix, kind, bad, message, typ string }{
		{"numeric", "NUMERIC", "'9e131071'", "value overflows numeric format", "NUMERIC"},
		{"float", "DOUBLE PRECISION", "1e200", "value out of range: overflow", "FLOAT8"},
	} {
		source, target := "bad_"+c.suffix, "target_"+c.suffix
		run(referenceExec("CREATE TABLE "+source+"(g INTEGER,v "+c.kind+")", 0))
		values := "(1,1),(1,3),(2," + c.bad + "),(2," + c.bad + ")"
		if c.suffix == "float" {
			values = "(1,1),(1,3),(2," + c.bad + "),(2,0)"
		}
		run(referenceExec("INSERT INTO "+source+" VALUES "+values, 4))
		run(referenceExec("CREATE TABLE "+target+"(g INTEGER,v "+c.kind+")", 0))
		run(referenceExec("INSERT INTO "+target+" VALUES (99,9)", 1))
		q := "SELECT g,avg(v) FROM " + source + " GROUP BY g ORDER BY g"
		numericAvgExecutionError(t, p, conn, recorder, q, c.message, true)
		numericAvgExecutionError(t, p, conn, recorder, "INSERT INTO "+target+" "+q, c.message, false)
		var sentinel any = "9"
		if c.typ == "FLOAT8" {
			sentinel = float64(9)
		}
		run(referenceSelect("SELECT g,v FROM "+target, []string{"g", "v"}, []string{"INT4", c.typ}, [][]any{{int64(99), sentinel}}))
	}
}

func numericAvgHeldRecovery(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
	for _, c := range []struct{ name, kind, typ, bad, message string }{
		{"held_n", "NUMERIC", "NUMERIC", "('9e131071'),('9e131071')", "value overflows numeric format"},
		{"held_d", "DOUBLE PRECISION", "FLOAT8", "(1e200),(0)", "value out of range: overflow"},
	} {
		run(referenceExec("CREATE TABLE "+c.name+"(v "+c.kind+")", 0))
		run(referenceExec("INSERT INTO "+c.name+" VALUES (1),(3)", 2))
		q := "SELECT avg(v) FROM " + c.name
		stmt, err := conn.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
			check := func(n string, f float64, null bool) {
				var want any = n
				if c.typ == "FLOAT8" {
					want = f
				}
				if null {
					want = nil
				}
				referenceRunStep(t, p.ctx, held, recorder, referenceSelect(q, []string{"avg"}, []string{c.typ}, [][]any{{want}}))
			}
			check("2.0000000000000000", 2, false)
			run(referenceExec("INSERT INTO "+c.name+" VALUES (8),(NULL)", 2))
			check("4.0000000000000000", 4, false)
			run(referenceExec("DELETE FROM "+c.name, 4))
			check("", 0, true)
			run(referenceExec("INSERT INTO "+c.name+" VALUES (NULL)", 1))
			check("", 0, true)
			run(referenceExec("DELETE FROM "+c.name, 1))
			run(referenceExec("INSERT INTO "+c.name+" VALUES "+c.bad, 2))
			s := referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: c.message})
			s.selectRows = true
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, held, recorder, s)
			run(referenceExec("DELETE FROM "+c.name, 2))
			if c.typ == "NUMERIC" {
				run(referenceExec("INSERT INTO "+c.name+" VALUES ('NaN')", 1))
				check("NaN", 0, false)
				run(referenceExec("DELETE FROM "+c.name, 1))
			}
			run(referenceExec("INSERT INTO "+c.name+" VALUES (1),(3)", 2))
			check("2.0000000000000000", 2, false)
		}()
	}
}

func TestReferenceNumericAvgLifecycle(t *testing.T) {
	numericAvgScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("SET IntervalStyle = 'postgres'", 0))
		run(referenceExec("CREATE TABLE grouped(g INTEGER,i INTEGER,d DOUBLE PRECISION,b BOOLEAN,v INTERVAL)", 0))
		run(referenceExec("INSERT INTO grouped VALUES (1,1,1,TRUE,'1 day'),(1,NULL,NULL,NULL,NULL),(1,3,3,FALSE,'3 days'),(2,NULL,NULL,NULL,NULL)", 4))
		numericAvgBoth(t, p, conn, recorder, referenceSelect("SELECT g,avg(i),avg(i),avg(d),count(i),bool_and(b),avg(v) FROM grouped GROUP BY g ORDER BY g", []string{"g", "avg", "avg", "avg", "count", "bool_and", "avg"}, []string{"INT4", "NUMERIC", "NUMERIC", "FLOAT8", "INT8", "BOOL", "INTERVAL"}, [][]any{{int64(1), "2.0000000000000000", "2.0000000000000000", float64(2), int64(2), false, "2 days"}, {int64(2), nil, nil, nil, int64(0), nil, nil}}))
		numericAvgBoth(t, p, conn, recorder, referenceSelect("SELECT g FROM grouped GROUP BY g HAVING avg(i)>1 ORDER BY avg(d)", []string{"g"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(referenceExec("CREATE VIEW avg_view AS SELECT g,avg(i) AS n,avg(d) AS d FROM grouped GROUP BY g", 0))
		numericAvgBoth(t, p, conn, recorder, referenceSelect("SELECT g,n,d FROM avg_view ORDER BY g", []string{"g", "n", "d"}, []string{"INT4", "NUMERIC", "FLOAT8"}, [][]any{{int64(1), "2.0000000000000000", float64(2)}, {int64(2), nil, nil}}))
		run(referenceExec("CREATE TABLE destination(g INTEGER,n NUMERIC,d DOUBLE PRECISION)", 0))
		for _, prepared := range []bool{false, true} {
			s := referenceExec("INSERT INTO destination SELECT g,avg(i),avg(d) FROM grouped GROUP BY g", 2)
			s.prepared = prepared
			run(s)
			s = referenceExec("INSERT INTO destination(n,d) SELECT avg(i),avg(d) FROM grouped WHERE FALSE", 1)
			s.prepared = prepared
			run(s)
			s = referenceExec("INSERT INTO destination SELECT g,avg(i),avg(d) FROM grouped WHERE FALSE GROUP BY g", 0)
			s.prepared = prepared
			run(s)
		}
		s := referenceSelect("SELECT g,n,d FROM destination", []string{"g", "n", "d"}, []string{"INT4", "NUMERIC", "FLOAT8"}, [][]any{{int64(1), "2.0000000000000000", float64(2)}, {int64(1), "2.0000000000000000", float64(2)}, {int64(2), nil, nil}, {int64(2), nil, nil}, {nil, nil, nil}, {nil, nil, nil}})
		s.bag = true
		run(s)
		numericAvgHeldRecovery(t, p, conn, recorder)
		numericAvgAtomicity(t, p, conn, recorder)
		// Nonfinite state is not a reason to stop evaluating later arguments.
		run(referenceExec("CREATE TABLE late(n NUMERIC,v TEXT)", 0))
		for _, special := range []string{"Infinity", "NaN"} {
			run(referenceExec("INSERT INTO late VALUES ('"+special+"','1'),(0,'oops')", 2))
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("SELECT avg(CASE WHEN v::integer=1 THEN n ELSE NULL END) FROM late", referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type integer: \"oops\""})
				s.selectRows = true
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
			}
			run(referenceExec("DELETE FROM late", 2))
		}
	})
}

func TestReferenceNumericAvgSmoke(t *testing.T) {
	numericAvgScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		numericAvgArithmetic(t, p, conn, recorder, pg)
		numericAvgHeldRecovery(t, p, conn, recorder)
		numericAvgAtomicity(t, p, conn, recorder)
		for _, c := range []struct {
			name, kind, values, typ string
			want                    any
			count                   int64
		}{
			{"third", "INTEGER", "(0),(-1),(0)", "NUMERIC", "-0.33333333333333333333", 3},
			{"maxint", "BIGINT", "(9223372036854775807),(9223372036854775807)", "NUMERIC", "9223372036854775807", 2},
			{"real", "REAL", "(0.1)", "FLOAT8", float64(0.10000000149011612), 1},
			{"cancel", "NUMERIC", "('9e131071'),('9e131071'),('-9e131071'),('-9e131071')", "NUMERIC", "0.00000000000000000000", 4},
		} {
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE smoke_"+c.name+"(v "+c.kind+")", 0))
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO smoke_"+c.name+" VALUES "+c.values, c.count))
			numericAvgBoth(t, p, conn, recorder, referenceSelect("SELECT avg(v) FROM smoke_"+c.name, []string{"avg"}, []string{c.typ}, [][]any{{c.want}}))
		}
	})
}
