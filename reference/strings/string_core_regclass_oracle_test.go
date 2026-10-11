package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringCoreRegclassDefaultBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
			run(fixture.Exec("CREATE TABLE regclass_source(v INT)", 0))
			run(fixture.Exec("INSERT INTO regclass_source VALUES(42)", 1))
			for _, prepared := range []bool{false, true} {
				name := "regclass_default_direct"
				if prepared {
					name = "regclass_default_prepared"
				}
				step := fixture.Exec("CREATE TABLE "+name+"(v TEXT DEFAULT ('x'::text||'regclass_source'::regclass))", 0)
				step.Prepared = prepared
				run(step)
				run(fixture.Exec("INSERT INTO "+name+" DEFAULT VALUES", 1))
				run(fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_source"}}))
				run(fixture.Select("SELECT v FROM regclass_source", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
				run(fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='"+name+"'", []string{"relname"}, []string{"NAME"}, [][]any{{name}}))
				run(fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='regclass_source'", []string{"relname"}, []string{"NAME"}, [][]any{{"regclass_source"}}))
				run(fixture.Exec("DROP TABLE "+name, 0))
			}
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE TABLE regclass_default_source(v INT)", 0),
				fixture.Exec("CREATE TABLE regclass_default_stored(v TEXT DEFAULT ('x'::text||'regclass_default_source'::regclass))", 0),
				fixture.Exec("INSERT INTO regclass_default_stored DEFAULT VALUES", 1),
				fixture.Select("SELECT v FROM regclass_default_stored", []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_default_source"}}),
				fixture.Prepared(fixture.Exec("INSERT INTO regclass_default_stored DEFAULT VALUES", 1)),
				fixture.Select("SELECT v FROM regclass_default_stored", []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_default_source"}, {"xregclass_default_source"}}),
			} {
				run(step)
			}
		})
	}
}
