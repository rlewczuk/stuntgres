package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func sqlPreparedScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "ROLLBACK")
			defer c.ExecContext(context.Background(), "DEALLOCATE ALL")
			body(t, p, b, c)
		})
	}
}

func sqlPreparedStep(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func sqlPreparedDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func sqlPreparedMissing(name string) fixture.Diagnostic {
	return sqlPreparedDiagnostic("26000", fmt.Sprintf("prepared statement %q does not exist", name))
}

func sqlPreparedPosition(query, token string) string {
	index := strings.Index(query, token)
	if index < 0 {
		panic("missing diagnostic position token")
	}
	return strconv.Itoa(utf8.RuneCountInString(query[:index]) + 1)
}

func sqlPreparedError(query string, diagnostic fixture.Diagnostic, prepared bool) fixture.Step {
	s := fixture.ErrorDiagnostic(query, diagnostic)
	s.Prepared = prepared
	s.ErrorStage = "execute"
	return s
}

func sqlPreparedScalar(query, name, typ string, value any) fixture.Step {
	return fixture.Select(query, []string{name}, []string{typ}, [][]any{{value}})
}

func sqlPreparedSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TABLE prep_t(v INTEGER,s TEXT,f BOOLEAN)", 0))
	sqlPreparedStep(t, p, b, c, fixture.Exec("INSERT INTO prep_t VALUES(7,'seven',TRUE),(8,'eight',FALSE)", 2))
}
