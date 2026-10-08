//go:build reference

package reference_test

import "testing"

func TestReferenceBooleanAggregateCallers(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE t(k TEXT,b BOOL)", 0), referenceExec("INSERT INTO t VALUES('T',TRUE),('F',FALSE),('M',TRUE),('M',FALSE),('N',NULL),(NULL,TRUE)", 6), referenceExec("CREATE TABLE empty(k TEXT,b BOOL)", 0), referenceExec("CREATE TABLE d(v INT)", 0), referenceExec("INSERT INTO d VALUES(1),(0)", 2), referenceExec("CREATE TABLE out(b BOOL)", 0), referenceExec("INSERT INTO out VALUES(TRUE)", 1), referenceExec("CREATE VIEW aggregated(a,o,e,u) AS SELECT bool_and(b),bool_or(b),every(b),every('true') FROM t", 0)} {
		p.step(t, s)
	}
	for _, s := range []referenceStep{
		referenceSelect("SELECT k FROM t GROUP BY k HAVING bool_or(b)", []string{"k"}, []string{"TEXT"}, [][]any{{"T"}, {"M"}, {nil}}),
		referenceSelect("SELECT k FROM t GROUP BY k HAVING every(b)", []string{"k"}, []string{"TEXT"}, [][]any{{"T"}, {nil}}),
		referenceSelect("SELECT k,bool_and(b) FROM t GROUP BY k ORDER BY bool_or(b)", []string{"k", "bool_and"}, []string{"TEXT", "BOOL"}, [][]any{{"T", true}, {"F", false}, {"M", false}, {"N", nil}, {nil, true}}),
		referenceSelect("SELECT COALESCE(bool_and(b),FALSE) AS answer FROM empty", []string{"answer"}, []string{"BOOL"}, [][]any{{false}}),
		referenceSelect("SELECT pg_catalog.bool_and(b),\"bool_or\"(b),every(b IS NULL) FROM t", []string{"bool_and", "bool_or", "every"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false}}),
		referenceSelect("SELECT a,o,e,u FROM aggregated", []string{"a", "o", "e", "u"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}}),
	} {
		s.bag = true
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, q := range []string{"SELECT bool_and(10/v=0) FROM d", "SELECT bool_or(10/v>0) FROM d", "INSERT INTO out SELECT bool_and(10/v=0) FROM d"} {
		s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
		s.selectRows = q[:6] == "SELECT"
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	p.step(t, referenceSelect("SELECT b FROM out", []string{"b"}, []string{"BOOL"}, [][]any{{true}}))
	for _, prepared := range []bool{false, true} {
		for _, s := range []referenceStep{referenceExec("INSERT INTO out SELECT bool_and(b) FROM t", 1), referenceExec("INSERT INTO out SELECT bool_and(b) FROM t GROUP BY k", 5), referenceExec("INSERT INTO out SELECT every(b) FROM empty", 1), referenceExec("INSERT INTO out SELECT bool_or(b) FROM empty GROUP BY k", 0)} {
			s.prepared = prepared
			p.step(t, s)
		}
	}
	s := referenceSelect("SELECT b FROM out", []string{"b"}, []string{"BOOL"}, [][]any{{true}, {false}, {true}, {false}, {false}, {nil}, {true}, {nil}, {false}, {true}, {false}, {false}, {nil}, {true}, {nil}})
	s.bag = true
	p.step(t, s)
}

func TestReferenceBooleanAggregateCapabilityBoundaries(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE m(b BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO m VALUES(TRUE),(FALSE),(NULL)", 3))
	for _, tc := range []struct {
		q    string
		rows [][]any
	}{
		{"SELECT bool_and(DISTINCT b) FROM m", [][]any{{false}}},
		{"SELECT bool_or(DISTINCT b) FROM m", [][]any{{true}}},
		{"SELECT every(DISTINCT b) FROM m", [][]any{{false}}},
		{"SELECT bool_and(b) FILTER (WHERE FALSE) FROM m", [][]any{{nil}}},
		{"SELECT bool_and(b) FILTER (WHERE NULL) FROM m", [][]any{{nil}}},
		{"SELECT bool_and(b) FILTER (WHERE b) FROM m", [][]any{{true}}},
		{"SELECT bool_and(b) FILTER (WHERE NOT b) FROM m", [][]any{{false}}},
		{"SELECT bool_and(b) FILTER (WHERE b IS NULL) FROM m", [][]any{{nil}}},
		{"SELECT every(ALL b) FROM m", [][]any{{false}}},
		{"SELECT bool_or(b ORDER BY b) FROM m", [][]any{{true}}},
		{"SELECT bool_and(TRUE)", [][]any{{true}}},
		{"SELECT bool_or(b) OVER () FROM m", [][]any{{true}, {true}, {true}}},
	} {
		for _, prepared := range []bool{false, true} {
			native := referenceError(tc.q, "0A000")
			native.selectRows = true
			native.prepared = prepared
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
			name := "bool_and"
			if len(tc.q) >= 14 && tc.q[7:14] == "bool_or" {
				name = "bool_or"
			}
			if tc.q[7:12] == "every" {
				name = "every"
			}
			oracle := referenceSelect(tc.q, []string{name}, []string{"BOOL"}, tc.rows)
			oracle.prepared = prepared
			oracle.bag = true
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, oracle)
		}
	}
}
