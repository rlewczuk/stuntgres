package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceStringCoreRegclassDefaultSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			stringCoreControlExec(t, backend.db, "CREATE TABLE regclass_source(v INT)")
			stringCoreControlExec(t, backend.db, "INSERT INTO regclass_source VALUES(42)")
			q := `CREATE TABLE regclass_default_boundary(v TEXT DEFAULT ('x'::text||'regclass_source'::regclass))`
			for _, prepared := range []bool{false, true} {
				step := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "regclass is unsupported in DEFAULT expressions", Position: strconv.Itoa(strings.Index(q, "'regclass_source'") + 1)})
				step.errorStage = "execute"
				if backend.pg {
					step = referenceExec(q, 0)
				}
				step.nonContext = true
				if prepared {
					step = referencePrepared(step)
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
				for _, sentinel := range []referenceStep{
					referenceSelect("SELECT v FROM regclass_source", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}),
					referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='regclass_source'", []string{"relname"}, []string{"NAME"}, [][]any{{"regclass_source"}}),
				} {
					sentinel.nonContext = true
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, sentinel)
				}
				if backend.pg {
					stringCoreControlExec(t, backend.db, "INSERT INTO regclass_default_boundary DEFAULT VALUES")
					value := referenceSelect("SELECT v FROM regclass_default_boundary", []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_source"}})
					value.nonContext = true
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, value)
					stringCoreControlExec(t, backend.db, "DROP TABLE regclass_default_boundary")
				} else {
					absent := referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='regclass_default_boundary'", []string{"relname"}, []string{"NAME"}, nil)
					absent.nonContext = true
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, absent)
				}
			}
		})
	}
}
