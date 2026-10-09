package reference_test

import (
	"database/sql"
	"testing"
)

func outerJoinsScenario(t *testing.T, body func(*testing.T, *referencePair, *sql.Conn, *referenceRecorder)) {
	t.Helper()
	outerJoinsBackendScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, backend referenceBackend) {
		body(t, p, conn, backend.recorder)
	})
}

func outerJoinsBackendScenario(t *testing.T, body func(*testing.T, *referencePair, *sql.Conn, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			for _, s := range []referenceStep{
				referenceExec("CREATE TABLE a(k INTEGER,l TEXT)", 0),
				referenceExec("CREATE TABLE b(k BIGINT,r TEXT)", 0),
				referenceExec("CREATE TABLE empty_a(k INTEGER,l TEXT)", 0),
				referenceExec("CREATE TABLE empty_b(k BIGINT,r TEXT)", 0),
				referenceExec("INSERT INTO a VALUES(1,'a'),(1,'a'),(2,'b'),(NULL,'n')", 4),
				referenceExec("INSERT INTO b VALUES(1,'x'),(1,'x'),(3,'y'),(NULL,'m')", 4),
			} {
				referenceRunStep(t, p.ctx, conn, backend.recorder, s)
			}
			body(t, p, conn, backend)
		})
	}
}

func outerJoinsCases(t *testing.T, p *referencePair, conn referenceExecutor, recorder *referenceRecorder, steps ...referenceStep) {
	t.Helper()
	for _, s := range steps {
		s.bag = true
		referenceRunStep(t, p.ctx, conn, recorder, s)
		referenceRunStep(t, p.ctx, conn, recorder, referencePrepared(s))
	}
}

func outerJoinsOnRows(kind string) [][]any {
	rows := [][]any{
		{int64(1), "a", int64(1), "x"}, {int64(1), "a", int64(1), "x"},
		{int64(1), "a", int64(1), "x"}, {int64(1), "a", int64(1), "x"},
	}
	if kind != "RIGHT" {
		rows = append(rows, []any{int64(2), "b", nil, nil}, []any{nil, "n", nil, nil})
	}
	if kind != "LEFT" {
		rows = append(rows, []any{nil, nil, int64(3), "y"}, []any{nil, nil, nil, "m"})
	}
	return rows
}

func outerJoinsMergedRows(kind string) [][]any {
	rows := [][]any{{int64(1), "a", "x"}, {int64(1), "a", "x"}, {int64(1), "a", "x"}, {int64(1), "a", "x"}}
	if kind != "RIGHT" {
		rows = append(rows, []any{int64(2), "b", nil}, []any{nil, "n", nil})
	}
	if kind != "LEFT" {
		rows = append(rows, []any{int64(3), nil, "y"}, []any{nil, nil, "m"})
	}
	return rows
}

func outerJoinsUnmatchedRows(kind string) [][]any {
	var rows [][]any
	if kind != "RIGHT" {
		rows = append(rows, []any{int64(1), "a", nil, nil}, []any{int64(1), "a", nil, nil}, []any{int64(2), "b", nil, nil}, []any{nil, "n", nil, nil})
	}
	if kind != "LEFT" {
		rows = append(rows, []any{nil, nil, int64(1), "x"}, []any{nil, nil, int64(1), "x"}, []any{nil, nil, int64(3), "y"}, []any{nil, nil, nil, "m"})
	}
	return rows
}

func outerJoinsSelect(query string, rows [][]any) referenceStep {
	return referenceSelect(query, []string{"k", "l", "k", "r"}, []string{"INT4", "TEXT", "INT8", "TEXT"}, rows)
}
