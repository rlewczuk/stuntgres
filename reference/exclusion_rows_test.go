package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceExclusionRows(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				c := concurrentIndexConn(t, p, b)
				run := func(s referenceStep) { exclusionReferenceRun(t, p, b, c, s) }
				run(referenceExec("CREATE TABLE t(v INT,note TEXT,CONSTRAINT ex EXCLUDE USING "+method+"(v WITH =))", 0))
				run(referenceExec("INSERT INTO t VALUES(1,'one'),(2,'two'),(NULL,'n1'),(NULL,'n2')", 4))
				q := "INSERT INTO t VALUES($1,$2)"
				stmt, err := c.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
				want := exclusionReferenceConflict("t", "ex", "v", "1", "1")
				for _, prepared := range []bool{false, true} {
					s := referenceErrorDiagnostic(q, want, int64(1), "duplicate")
					s.prepared, s.errorStage = prepared, "execute"
					run(s)
				}
				exclusionReferenceRun(t, p, b, held, referenceErrorDiagnostic(q, want, int64(1), "held"))
				exclusionReferenceRun(t, p, b, held, referenceExec(q, 1, int64(3), "three"))
				run(referenceErrorDiagnostic("INSERT INTO t VALUES(4,'atomic'),(1,'bad')", want))
				rows := referenceSelect("SELECT v,note FROM t ORDER BY v NULLS LAST,note", []string{"v", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}, {nil, "n1"}, {nil, "n2"}})
				run(rows)
				run(referencePrepared(rows))
				run(referenceExec("UPDATE t SET note='changed' WHERE v=1", 1))
				run(referenceExec("DELETE FROM t WHERE v=1", 1))
				exclusionReferenceRun(t, p, b, held, referenceExec(q, 1, int64(1), "fresh"))
			})
		})
	}
}

func TestReferenceExclusionExpressionsPredicate(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { exclusionReferenceRun(t, p, b, b.db, s) }
				run(referenceExec("CREATE TABLE t(id INT,note TEXT,active BOOL,CONSTRAINT ex EXCLUDE USING "+method+"(lower(note) WITH =) WHERE(active))", 0))
				run(referenceExec("INSERT INTO t VALUES(1,'A',TRUE),(2,'a',FALSE),(3,'a',NULL),(4,NULL,TRUE),(5,NULL,TRUE)", 5))
				want := exclusionReferenceConflict("t", "ex", "lower(note)", "a", "a")
				run(referencePrepared(referenceErrorDiagnostic("INSERT INTO t VALUES(6,'a',TRUE)", want)))
				run(referenceErrorDiagnostic("UPDATE t SET active=TRUE WHERE id=2", want))
				run(referenceSelect("SELECT id,note,active FROM t ORDER BY id", []string{"id", "note", "active"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "A", true}, {int64(2), "a", false}, {int64(3), "a", nil}, {int64(4), nil, true}, {int64(5), nil, true}}))
				run(referenceExec("UPDATE t SET active=FALSE WHERE id=1", 1))
				run(referenceExec("UPDATE t SET active=TRUE WHERE id=2", 1))
				run(referenceExec("CREATE TABLE skipped(v INT,active BOOL,EXCLUDE USING "+method+"((10/v) WITH =) WHERE(active))", 0))
				run(referenceExec("INSERT INTO skipped VALUES(0,FALSE),(0,NULL)", 2))
				run(referenceErrorDiagnostic("INSERT INTO skipped VALUES(0,TRUE)", controlsReferenceDiagnostic("22012", "division by zero")))
				run(referenceSelect("SELECT v,active FROM skipped ORDER BY active NULLS LAST", []string{"v", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), false}, {int64(0), nil}}))
			})
		})
	}
	t.Run("composite", func(t *testing.T) {
		exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			run := func(s referenceStep) { exclusionReferenceRun(t, p, b, b.db, s) }
			run(referenceExec("CREATE TABLE t(a INT,b TEXT,CONSTRAINT ex EXCLUDE(a WITH =,b WITH =))", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'x'),(1,'y'),(NULL,'x'),(NULL,'x'),(1,NULL),(1,NULL)", 6))
			run(referenceErrorDiagnostic("INSERT INTO t VALUES(1,'x')", exclusionReferenceConflict("t", "ex", "a, b", "1, x", "1, x")))
			s := referenceSelect("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(1), "y"}, {nil, "x"}, {nil, "x"}, {int64(1), nil}, {int64(1), nil}})
			s.bag = true
			run(s)
		})
	})
}

func TestReferenceExclusionDefinitions(t *testing.T) {
	for _, properties := range []string{"", "DEFERRABLE", "DEFERRABLE INITIALLY IMMEDIATE", "DEFERRABLE INITIALLY DEFERRED", "INITIALLY DEFERRED", "NOT DEFERRABLE INITIALLY IMMEDIATE", "DEFERRABLE DEFERRABLE INITIALLY DEFERRED INITIALLY DEFERRED"} {
		t.Run(properties, func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { exclusionReferenceRun(t, p, b, b.db, s) }
				run(referencePrepared(referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) "+properties+")", 0)))
			})
		})
	}
}

func TestReferenceExclusionTypedEquality(t *testing.T) {
	for _, c := range []struct{ typ, first, second, rendered string }{
		{"NUMERIC", "1.0", "1.00", "1.00"},
		{"DOUBLE PRECISION", "'NaN'", "'NaN'", "NaN"},
		{"CHAR(3)", "'x'", "'x '", "x  "},
		{"TEXT", "'x'", "'x'", "x"},
		{"UUID", "'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11'", "'A0EEBC999C0B4EF8BB6D6BB9BD380A11'", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"},
	} {
		for _, method := range []string{"btree", "hash"} {
			t.Run(fmt.Sprintf("%s/%s", c.typ, method), func(t *testing.T) {
				exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					run := func(s referenceStep) { exclusionReferenceRun(t, p, b, b.db, s) }
					run(referenceExec("CREATE TABLE t(v "+c.typ+",CONSTRAINT ex EXCLUDE USING "+method+"(v WITH =))", 0))
					run(referenceExec("INSERT INTO t VALUES("+c.first+")", 1))
					existing := c.rendered
					if c.typ == "NUMERIC" {
						existing = "1.0"
					}
					run(referencePrepared(referenceErrorDiagnostic("INSERT INTO t VALUES("+c.second+")", exclusionReferenceConflict("t", "ex", "v", c.rendered, existing))))
					run(referenceSelect("SELECT count(*) AS n FROM t", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				})
			})
		}
	}
}
