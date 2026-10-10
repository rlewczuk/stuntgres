package reference_test

import "testing"

func TestReferenceStringCoreRegclassDefaultBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(step referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, step) }
			run(referenceExec("CREATE TABLE regclass_source(v INT)", 0))
			run(referenceExec("INSERT INTO regclass_source VALUES(42)", 1))
			for _, prepared := range []bool{false, true} {
				name := "regclass_default_direct"
				if prepared {
					name = "regclass_default_prepared"
				}
				step := referenceExec("CREATE TABLE "+name+"(v TEXT DEFAULT ('x'::text||'regclass_source'::regclass))", 0)
				step.prepared = prepared
				run(step)
				run(referenceExec("INSERT INTO "+name+" DEFAULT VALUES", 1))
				run(referenceSelect("SELECT v FROM "+name, []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_source"}}))
				run(referenceSelect("SELECT v FROM regclass_source", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
				run(referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='"+name+"'", []string{"relname"}, []string{"NAME"}, [][]any{{name}}))
				run(referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='regclass_source'", []string{"relname"}, []string{"NAME"}, [][]any{{"regclass_source"}}))
				run(referenceExec("DROP TABLE "+name, 0))
			}
			for _, step := range []referenceStep{
				referenceExec("CREATE TABLE regclass_default_source(v INT)", 0),
				referenceExec("CREATE TABLE regclass_default_stored(v TEXT DEFAULT ('x'::text||'regclass_default_source'::regclass))", 0),
				referenceExec("INSERT INTO regclass_default_stored DEFAULT VALUES", 1),
				referenceSelect("SELECT v FROM regclass_default_stored", []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_default_source"}}),
				referencePrepared(referenceExec("INSERT INTO regclass_default_stored DEFAULT VALUES", 1)),
				referenceSelect("SELECT v FROM regclass_default_stored", []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_default_source"}, {"xregclass_default_source"}}),
			} {
				run(step)
			}
		})
	}
}
