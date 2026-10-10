package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"
)

// Rows, types and diagnostics are independently asserted against PostgreSQL 18.0.
func groupingSetsScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn, func(referenceStep))) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
			body(t, p, b, c, run)
		})
	}
}

func groupingSetsFixture(run func(referenceStep)) {
	run(referenceExec("CREATE TABLE gs(a INT,b INT,v INT)", 0))
	run(referenceExec("INSERT INTO gs VALUES(1,10,2),(1,20,3),(NULL,10,5)", 3))
	run(referenceExec("CREATE TABLE gs_empty(a INT,b INT,v INT)", 0))
}

func groupingSetsBoth(run func(referenceStep), s referenceStep) {
	run(s)
	run(referencePrepared(s))
}

func groupingSetsBag(q string, columns, types []string, rows [][]any) referenceStep {
	s := referenceSelect(q, columns, types, rows)
	s.bag = true
	return s
}

func groupingSetsRollupRows() [][]any {
	return [][]any{
		{int64(1), int64(10), int64(0), int64(1), int64(2)},
		{int64(1), int64(20), int64(0), int64(1), int64(3)},
		{nil, int64(10), int64(0), int64(1), int64(5)},
		{int64(1), nil, int64(1), int64(2), int64(5)},
		{nil, nil, int64(1), int64(1), int64(5)},
		{nil, nil, int64(3), int64(3), int64(10)},
	}
}

func groupingSetsLevel(q string, cube bool) referenceStep {
	rows := groupingSetsRollupRows()
	if cube {
		rows = append(rows, []any{nil, int64(10), int64(2), int64(2), int64(7)}, []any{nil, int64(20), int64(2), int64(1), int64(3)})
	}
	return groupingSetsBag(q, []string{"a", "b", "grouping", "count", "sum"}, []string{"INT4", "INT4", "INT4", "INT8", "INT8"}, rows)
}

// token identifies a diagnostic's character location, not a byte offset.
// Existing witnesses passed pinned18.0; new callers require their own discovery.
func groupingSetsError(q, code, message, token string) referenceStep {
	position := ""
	if token != "" {
		i := strings.Index(q, token)
		if i < 0 {
			panic("missing diagnostic token: " + token)
		}
		position = referencePosition(utf8.RuneCountInString(q[:i]) + 1)
	}
	s := referenceImplicitError(q, referenceDiagnostic{Code: code, Message: message, Position: position})
	s.selectRows = true
	return s
}

func groupingSetsAnalysisError(run func(referenceStep), s referenceStep) {
	s.errorStage = "execute"
	run(s)
	s.errorStage = "prepare"
	run(referencePrepared(s))
}
