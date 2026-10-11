package fixture

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"
)

// Rows, types and diagnostics are independently asserted against PostgreSQL 18.0.
func GroupingSetsScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn, func(Step))) {
	t.Helper()
	p := NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s Step) { RunStep(t, p.Ctx, c, b.Recorder, s) }
			body(t, p, b, c, run)
		})
	}
}

func GroupingSetsFixture(run func(Step)) {
	run(Exec("CREATE TABLE gs(a INT,b INT,v INT)", 0))
	run(Exec("INSERT INTO gs VALUES(1,10,2),(1,20,3),(NULL,10,5)", 3))
	run(Exec("CREATE TABLE gs_empty(a INT,b INT,v INT)", 0))
}

func GroupingSetsBoth(run func(Step), s Step) {
	run(s)
	run(Prepared(s))
}

func GroupingSetsBag(q string, columns, types []string, rows [][]any) Step {
	s := Select(q, columns, types, rows)
	s.Bag = true
	return s
}

func GroupingSetsRollupRows() [][]any {
	return [][]any{
		{int64(1), int64(10), int64(0), int64(1), int64(2)},
		{int64(1), int64(20), int64(0), int64(1), int64(3)},
		{nil, int64(10), int64(0), int64(1), int64(5)},
		{int64(1), nil, int64(1), int64(2), int64(5)},
		{nil, nil, int64(1), int64(1), int64(5)},
		{nil, nil, int64(3), int64(3), int64(10)},
	}
}

func GroupingSetsLevel(q string, cube bool) Step {
	rows := GroupingSetsRollupRows()
	if cube {
		rows = append(rows, []any{nil, int64(10), int64(2), int64(2), int64(7)}, []any{nil, int64(20), int64(2), int64(1), int64(3)})
	}
	return GroupingSetsBag(q, []string{"a", "b", "grouping", "count", "sum"}, []string{"INT4", "INT4", "INT4", "INT8", "INT8"}, rows)
}

// token identifies a diagnostic's character location, not a byte offset.
// Existing witnesses passed pinned18.0; new callers require their own discovery.
func GroupingSetsError(q, code, message, token string) Step {
	position := ""
	if token != "" {
		i := strings.Index(q, token)
		if i < 0 {
			panic("missing diagnostic token: " + token)
		}
		position = Position(utf8.RuneCountInString(q[:i]) + 1)
	}
	s := ImplicitError(q, Diagnostic{Code: code, Message: message, Position: position})
	s.SelectRows = true
	return s
}

func GroupingSetsAnalysisError(run func(Step), s Step) {
	s.ErrorStage = "execute"
	run(s)
	s.ErrorStage = "prepare"
	run(Prepared(s))
}
