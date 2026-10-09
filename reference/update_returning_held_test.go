package reference_test

import "testing"

func TestReferenceUpdateReturningRepeatedParameterOrder(t *testing.T) {
	for _, q := range []string{"UPDATE t SET id=$1 WHERE id=$1 RETURNING $1 AS typed,$1::int4 AS repeated", "UPDATE t SET id=2 RETURNING $1::int4 AS typed,$1 AS repeated"} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			t.Run(backend.name, func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(2),(2)", 2))
				held, err := backend.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				x := referenceHeldStatement{referenceExecutor: backend.db, statement: held, query: q}
				for range 2 {
					s := referenceSelect(q, []string{"typed", "repeated"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}, {int64(2), int64(2)}}, 2)
					s.bag = true
					referenceRunStep(t, p.ctx, x, backend.recorder, s)
				}
			})
		}
	}
}

func TestReferenceUpdateReturningVersionColumns(t *testing.T) {
	for _, name := range []string{"old", "new"} {
		p := newReferenceScenario(t)
		p.step(t, referenceExec("CREATE TABLE "+name+"(a INT,old INT,new INT)", 0))
		p.step(t, referenceExec("INSERT INTO "+name+" VALUES(10,20,30)", 1))
		p.step(t, referenceSelect("UPDATE "+name+" SET a=a+1 RETURNING WITH (OLD AS o) "+name+".a,o.a,old,new", []string{"a", "a", "old", "new"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(11), int64(10), int64(20), int64(30)}}))
	}
}

func TestReferenceUpdateReturningSelfFromDiagnostic(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1)", 1))
	p.step(t, referenceImplicitError("UPDATE t SET id=2 FROM t RETURNING id", referenceDiagnostic{Code: "42712", Message: `table name "t" specified more than once`}))
	p.step(t, referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
}
