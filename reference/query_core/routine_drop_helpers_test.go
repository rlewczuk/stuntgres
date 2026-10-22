package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func routineDropScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *[]fixture.Diagnostic)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, _ := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			body(t, p, b, c, &notices)
		})
	}
}

func routineDropError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, want fixture.Diagnostic) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, want)
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func routineDropNotice(message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message}
}

func routineDropNotices(t *testing.T, got *[]fixture.Diagnostic, want ...fixture.Diagnostic) {
	t.Helper()
	if len(*got) != len(want) || (len(want) != 0 && !reflect.DeepEqual(*got, want)) {
		t.Errorf("routine DROP notices\n got: %#v\nwant: %#v", *got, want)
	}
	*got = nil
}

func routineDropHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, stmt *sql.Stmt, q, typ string, value any) {
	t.Helper()
	s := fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{value}})
	s.CheckMetadata = routineScalarMetadata(b, "v", typ, false, false)
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
}
