package reference_test

import (
	"testing"
)

func TestReferenceUpdateReturningMixedStars(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT,v TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,10,20,'one'),(2,30,40,NULL),(2,30,40,NULL)", 3))
	q := "UPDATE t SET a=b,b=a WHERE id=2 RETURNING *,old.*,new.*,t.*,public.t.*"
	names := []string{"id", "a", "b", "v", "id", "a", "b", "v", "id", "a", "b", "v", "id", "a", "b", "v", "id", "a", "b", "v"}
	types := []string{"INT4", "INT4", "INT4", "TEXT", "INT4", "INT4", "INT4", "TEXT", "INT4", "INT4", "INT4", "TEXT", "INT4", "INT4", "INT4", "TEXT", "INT4", "INT4", "INT4", "TEXT"}
	first := []any{int64(2), int64(40), int64(30), nil, int64(2), int64(30), int64(40), nil, int64(2), int64(40), int64(30), nil, int64(2), int64(40), int64(30), nil, int64(2), int64(40), int64(30), nil}
	direct := referenceSelect(q, names, types, [][]any{first, first})
	direct.bag = true
	p.step(t, direct)
	for _, backend := range p.backends() {
		db := backend.db
		held, err := db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(backend.name, err)
		}
		second := []any{int64(2), int64(30), int64(40), nil, int64(2), int64(40), int64(30), nil, int64(2), int64(30), int64(40), nil, int64(2), int64(30), int64(40), nil, int64(2), int64(30), int64(40), nil}
		step := referenceSelect(q, names, types, [][]any{second, second})
		step.bag = true
		record := backend.recorder
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: db, statement: held, query: q}, record, step)
		if err := held.Close(); err != nil {
			t.Fatal(backend.name, err)
		}
	}
	final := referenceSelect("SELECT id,a,b,v FROM t", []string{"id", "a", "b", "v"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(10), int64(20), "one"}, {int64(2), int64(30), int64(40), nil}, {int64(2), int64(30), int64(40), nil}})
	final.bag = true
	p.step(t, final)
}

func TestReferenceUpdateReturningVersionNamedTargets(t *testing.T) {
	for _, target := range []string{"old", "new"} {
		t.Run(target, func(t *testing.T) {
			p := newReferenceScenario(t)
			p.step(t, referenceExec("CREATE TABLE "+target+"(id INT NOT NULL,a INT)", 0))
			p.step(t, referenceExec("INSERT INTO "+target+" VALUES(1,10),(1,10)", 2))
			q := "UPDATE " + target + " SET a=a+1 RETURNING WITH (OLD AS o) " + target + ".a,o.a"
			names := []string{"a", "a"}
			types := []string{"INT4", "INT4"}
			first := []any{int64(11), int64(10)}
			second := []any{int64(12), int64(11)}
			if target == "old" {
				q += ",new.a"
				names = append(names, "a")
				types = append(types, "INT4")
				first = append(first, int64(11))
				second = append(second, int64(12))
			}
			direct := referenceSelect(q, names, types, [][]any{first, first})
			direct.bag = true
			p.step(t, direct)
			for _, backend := range p.backends() {
				db := backend.db
				held, err := db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(backend.name, err)
				}
				step := referenceSelect(q, names, types, [][]any{second, second})
				step.bag = true
				record := backend.recorder
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: db, statement: held, query: q}, record, step)
				if err := held.Close(); err != nil {
					t.Fatal(backend.name, err)
				}
			}
			final := referenceSelect("SELECT id,a FROM "+target, []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(12)}, {int64(1), int64(12)}})
			final.bag = true
			p.step(t, final)
		})
	}
}
