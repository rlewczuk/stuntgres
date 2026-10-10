package reference_test

import "testing"

func TestReferenceStringCoreRegclassDefaultSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			stringCoreControlExec(t, backend.db, "CREATE TABLE regclass_source(v INT)")
			stringCoreControlExec(t, backend.db, "INSERT INTO regclass_source VALUES(42)")
			for _, prepared := range []bool{false, true} {
				name := "regclass_default_direct"
				if prepared {
					name = "regclass_default_prepared"
				}
				q := "CREATE TABLE " + name + "(v TEXT DEFAULT ('x'::text||'regclass_source'::regclass))"
				step := referenceExec(q, 0)
				step.nonContext = true
				step.prepared = prepared
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
				for _, sentinel := range []referenceStep{
					referenceSelect("SELECT v FROM regclass_source", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}),
					referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='regclass_source'", []string{"relname"}, []string{"NAME"}, [][]any{{"regclass_source"}}),
				} {
					sentinel.nonContext = true
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, sentinel)
				}
				stringCoreControlExec(t, backend.db, "INSERT INTO "+name+" DEFAULT VALUES")
				value := referenceSelect("SELECT v FROM "+name, []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_source"}})
				value.nonContext = true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, value)
				stringCoreControlExec(t, backend.db, "DROP TABLE "+name)
			}
		})
	}
}
