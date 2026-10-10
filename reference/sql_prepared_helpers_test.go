package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func sqlPreparedScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			defer c.ExecContext(context.Background(), "ROLLBACK")
			defer c.ExecContext(context.Background(), "DEALLOCATE ALL")
			body(t, p, b, c)
		})
	}
}

func sqlPreparedStep(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, s referenceStep) {
	t.Helper()
	referenceRunStep(t, p.ctx, c, b.recorder, s)
}

func sqlPreparedDiagnostic(code, message string) referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func sqlPreparedMissing(name string) referenceDiagnostic {
	return sqlPreparedDiagnostic("26000", fmt.Sprintf("prepared statement %q does not exist", name))
}

func sqlPreparedPosition(query, token string) string {
	index := strings.Index(query, token)
	if index < 0 { panic("missing diagnostic position token") }
	return strconv.Itoa(utf8.RuneCountInString(query[:index]) + 1)
}

func sqlPreparedError(query string, diagnostic referenceDiagnostic, prepared bool) referenceStep {
	s := referenceErrorDiagnostic(query, diagnostic)
	s.prepared = prepared
	s.errorStage = "execute"
	return s
}

func sqlPreparedScalar(query, name, typ string, value any) referenceStep {
	return referenceSelect(query, []string{name}, []string{typ}, [][]any{{value}})
}

func sqlPreparedSetup(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	sqlPreparedStep(t, p, b, c, referenceExec("CREATE TABLE prep_t(v INTEGER,s TEXT,f BOOLEAN)", 0))
	sqlPreparedStep(t, p, b, c, referenceExec("INSERT INTO prep_t VALUES(7,'seven',TRUE),(8,'eight',FALSE)", 2))
}
