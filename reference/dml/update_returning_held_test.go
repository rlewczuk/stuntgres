package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningRepeatedParameterOrder(t *testing.T) {
	for _, q := range []string{"UPDATE t SET id=$1 WHERE id=$1 RETURNING $1 AS typed,$1::int4 AS repeated", "UPDATE t SET id=2 RETURNING $1::int4 AS typed,$1 AS repeated"} {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(2),(2)", 2))
				held, err := backend.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				x := fixture.HeldStatement{Executor: backend.DB, Statement: held, SQL: q}
				for range 2 {
					s := fixture.Select(q, []string{"typed", "repeated"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}, {int64(2), int64(2)}}, 2)
					s.Bag = true
					fixture.RunStep(t, p.Ctx, x, backend.Recorder, s)
				}
			})
		}
	}
}

func TestReferenceUpdateReturningVersionColumns(t *testing.T) {
	for _, name := range []string{"old", "new"} {
		p := fixture.NewScenario(t)
		p.Step(t, fixture.Exec("CREATE TABLE "+name+"(a INT,old INT,new INT)", 0))
		p.Step(t, fixture.Exec("INSERT INTO "+name+" VALUES(10,20,30)", 1))
		p.Step(t, fixture.Select("UPDATE "+name+" SET a=a+1 RETURNING WITH (OLD AS o) "+name+".a,o.a,old,new", []string{"a", "a", "old", "new"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(11), int64(10), int64(20), int64(30)}}))
	}
}

func TestReferenceUpdateReturningSelfFromDiagnostic(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1)", 1))
	p.Step(t, fixture.ImplicitError("UPDATE t SET id=2 FROM t RETURNING id", fixture.Diagnostic{Code: "42712", Message: `table name "t" specified more than once`}))
	p.Step(t, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
}
