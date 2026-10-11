package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringCoreRegclassDefaultSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.StringCoreControlExec(t, backend.DB, "CREATE TABLE regclass_source(v INT)")
			fixture.StringCoreControlExec(t, backend.DB, "INSERT INTO regclass_source VALUES(42)")
			for _, prepared := range []bool{false, true} {
				name := "regclass_default_direct"
				if prepared {
					name = "regclass_default_prepared"
				}
				q := "CREATE TABLE " + name + "(v TEXT DEFAULT ('x'::text||'regclass_source'::regclass))"
				step := fixture.Exec(q, 0)
				step.NonContext = true
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
				for _, sentinel := range []fixture.Step{
					fixture.Select("SELECT v FROM regclass_source", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}),
					fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='regclass_source'", []string{"relname"}, []string{"NAME"}, [][]any{{"regclass_source"}}),
				} {
					sentinel.NonContext = true
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, sentinel)
				}
				fixture.StringCoreControlExec(t, backend.DB, "INSERT INTO "+name+" DEFAULT VALUES")
				value := fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_source"}})
				value.NonContext = true
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, value)
				fixture.StringCoreControlExec(t, backend.DB, "DROP TABLE "+name)
			}
		})
	}
}
