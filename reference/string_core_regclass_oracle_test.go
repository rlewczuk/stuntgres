package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceStringCoreRegclassDefaultBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			if !backend.pg {
				run := func(step referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, step) }
				run(referenceExec("CREATE TABLE regclass_source(v INT)", 0))
				run(referenceExec("INSERT INTO regclass_source VALUES(42)", 1))
				q := `CREATE TABLE regclass_default_boundary(v TEXT DEFAULT ('x'::text||'regclass_source'::regclass))`
				step := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "regclass is unsupported in DEFAULT expressions", Position: strconv.Itoa(strings.Index(q, "'regclass_source'") + 1)})
				step.errorStage = "execute"
				for _, prepared := range []bool{false, true} {
					next := step
					if prepared {
						next = referencePrepared(next)
					}
					run(next)
					run(referenceSelect("SELECT v FROM regclass_source", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
					run(referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='regclass_default_boundary'", []string{"relname"}, []string{"NAME"}, nil))
					run(referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='regclass_source'", []string{"relname"}, []string{"NAME"}, [][]any{{"regclass_source"}}))
				}
			} else {
				run := func(step referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, step) }
				run(referenceExec("CREATE TABLE regclass_source(v INT)", 0))
				run(referenceExec("INSERT INTO regclass_source VALUES(42)", 1))
				q := `CREATE TABLE regclass_default_boundary(v TEXT DEFAULT ('x'::text||'regclass_source'::regclass))`
				for _, prepared := range []bool{false, true} {
					step := referenceExec(q, 0)
					if prepared {
						step = referencePrepared(step)
					}
					run(step)
					run(referenceExec("INSERT INTO regclass_default_boundary DEFAULT VALUES", 1))
					run(referenceSelect("SELECT v FROM regclass_default_boundary", []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_source"}}))
					run(referenceSelect("SELECT v FROM regclass_source", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
					run(referenceExec("DROP TABLE regclass_default_boundary", 0))
				}
				for _, s := range []referenceStep{
					referenceExec("CREATE TABLE regclass_default_source(v INT)", 0),
					referenceExec(`CREATE TABLE regclass_default_stored(v TEXT DEFAULT ('x'::text||'regclass_default_source'::regclass))`, 0),
					referenceExec("INSERT INTO regclass_default_stored DEFAULT VALUES", 1),
					referenceSelect("SELECT v FROM regclass_default_stored", []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_default_source"}}),
					referencePrepared(referenceExec("INSERT INTO regclass_default_stored DEFAULT VALUES", 1)),
					referenceSelect("SELECT v FROM regclass_default_stored", []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_default_source"}, {"xregclass_default_source"}}),
				} {
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
		})
	}
}
