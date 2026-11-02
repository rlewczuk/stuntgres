package reference_test

import (
	"database/sql"
	"os"
	"testing"

	"stuntgres/reference/fixture"
)

func TestMain(m *testing.M) { os.Exit(fixture.Main(m)) }

func xml2Case(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.Recorder = recorder
			defer func() {
				if len(notices) != 0 {
					t.Errorf("unexpected XML2 notices=%#v", notices)
				}
			}()
			body(t, p, b, c)
		})
	}
}

func xml2Install(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION xml2", 0))
}

func xml2Both(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
	}
}

func xml2Scalars(document, path any, text, number, boolean any) fixture.Step {
	return fixture.Select("SELECT xpath_string($1::text,$2::text) AS s, xpath_number($1::text,$2::text) AS n, xpath_bool($1::text,$2::text) AS b",
		[]string{"s", "n", "b"}, []string{"TEXT", "FLOAT4", "BOOL"}, [][]any{{text, number, boolean}}, document, path)
}

func xml2Held(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, steps ...fixture.Step) {
	t.Helper()
	q := steps[0].Query
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, s := range steps {
		fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
	}
}

func xml2Error(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func xml2Compile(detail string) fixture.Diagnostic {
	d := xml2Error("10608", "XPath Syntax Error")
	d.Detail = detail
	return d
}

func xml2Failure(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string, want fixture.Diagnostic, prepareFailure bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, want, args...)
		s.SelectRows = true
		s.Prepared = prepared
		s.ErrorStage = "execute"
		if prepared && prepareFailure {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
	}
}
