package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func orderedSetScenario(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle='postgres'", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET lc_monetary='C'", 0))
			body(t, p, c, b)
		})
	}
}

func orderedSetBoth(t *testing.T, p *fixture.Pair, c fixture.Executor, b fixture.Backend, s fixture.Step) {
	t.Helper()
	stage:=s.ErrorStage
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		if s.State!=""&&stage=="" {
			s.ErrorStage="execute"
			if prepared {s.ErrorStage="prepare"}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		if s.State!="" {
			exec:=s
			exec.SelectRows=false
			fixture.RunStep(t,p.Ctx,c,b.Recorder,exec)
		}
	}
}

func orderedSetValue(q, typ string, value any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{value}})
}

func orderedSetSetup(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE os_input(x INT,p FLOAT8)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO os_input VALUES(40,.75),(10,.25),(NULL,NULL),(20,.25),(30,.75)", 5))
}

func orderedSetDiagnostic(q, code, message, token, hint string) fixture.Step {
	d := fixture.Diagnostic{Code: code, Message: message, Hint: hint}
	if token != "" {
		i := strings.Index(q, token)
		if i < 0 { panic("diagnostic token absent") }
		d.Position = fixture.Position(utf8.RuneCountInString(q[:i])+1)
	}
	s := fixture.ImplicitError(q, d)
	s.SelectRows = true
	return s
}

func orderedSetRuntimeError(t *testing.T, p *fixture.Pair, c fixture.Executor, b fixture.Backend, q, code, message string) {
	t.Helper()
	s := orderedSetDiagnostic(q, code, message, "", "")
	s.ErrorStage = "execute"
	orderedSetBoth(t, p, c, b, s)
}
