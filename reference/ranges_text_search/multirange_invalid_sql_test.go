package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMultirangeInvalidSQL(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			q := "CREATE TABLE invalid(v int4multirange))"
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: strconv.Itoa(len(q))}
			s := fixture.ErrorDiagnostic(q, want)
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
		}
		// Successful same-name CREATE proves the valid prefix was not executed.
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE invalid(v int4multirange)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO invalid VALUES ('{[7,8)}')", 1))
		for _, prepared := range []bool{false, true} {
			q := "INSERT INTO invalid VALUES ('{[1,4)}'))"
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: strconv.Itoa(strings.LastIndex(q, ")") + 1)}
			s := fixture.ErrorDiagnostic(q, want)
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, rangeSelect(backend, "SELECT v FROM invalid", []string{"v"}, []string{"INT4MULTIRANGE"}, [][]any{{"{[7,8)}"}}, []bool{true}))
	}
}
