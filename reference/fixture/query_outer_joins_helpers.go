package fixture

import (
	"database/sql"
	"testing"
)

func OuterJoinsScenario(t *testing.T, body func(*testing.T, *Pair, *sql.Conn, *Recorder)) {
	t.Helper()
	OuterJoinsBackendScenario(t, func(t *testing.T, p *Pair, conn *sql.Conn, backend Backend) {
		body(t, p, conn, backend.Recorder)
	})
}

func OuterJoinsBackendScenario(t *testing.T, body func(*testing.T, *Pair, *sql.Conn, Backend)) {
	t.Helper()
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			for _, s := range []Step{
				Exec("CREATE TABLE a(k INTEGER,l TEXT)", 0),
				Exec("CREATE TABLE b(k BIGINT,r TEXT)", 0),
				Exec("CREATE TABLE empty_a(k INTEGER,l TEXT)", 0),
				Exec("CREATE TABLE empty_b(k BIGINT,r TEXT)", 0),
				Exec("INSERT INTO a VALUES(1,'a'),(1,'a'),(2,'b'),(NULL,'n')", 4),
				Exec("INSERT INTO b VALUES(1,'x'),(1,'x'),(3,'y'),(NULL,'m')", 4),
			} {
				RunStep(t, p.Ctx, conn, backend.Recorder, s)
			}
			body(t, p, conn, backend)
		})
	}
}

func OuterJoinsCases(t *testing.T, p *Pair, conn Executor, recorder *Recorder, steps ...Step) {
	t.Helper()
	for _, s := range steps {
		s.Bag = true
		RunStep(t, p.Ctx, conn, recorder, s)
		RunStep(t, p.Ctx, conn, recorder, Prepared(s))
	}
}

func OuterJoinsOnRows(kind string) [][]any {
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

func OuterJoinsMergedRows(kind string) [][]any {
	rows := [][]any{{int64(1), "a", "x"}, {int64(1), "a", "x"}, {int64(1), "a", "x"}, {int64(1), "a", "x"}}
	if kind != "RIGHT" {
		rows = append(rows, []any{int64(2), "b", nil}, []any{nil, "n", nil})
	}
	if kind != "LEFT" {
		rows = append(rows, []any{int64(3), nil, "y"}, []any{nil, nil, "m"})
	}
	return rows
}

func OuterJoinsUnmatchedRows(kind string) [][]any {
	var rows [][]any
	if kind != "RIGHT" {
		rows = append(rows, []any{int64(1), "a", nil, nil}, []any{int64(1), "a", nil, nil}, []any{int64(2), "b", nil, nil}, []any{nil, "n", nil, nil})
	}
	if kind != "LEFT" {
		rows = append(rows, []any{nil, nil, int64(1), "x"}, []any{nil, nil, int64(1), "x"}, []any{nil, nil, int64(3), "y"}, []any{nil, nil, nil, "m"})
	}
	return rows
}

func OuterJoinsSelect(query string, rows [][]any) Step {
	return Select(query, []string{"k", "l", "k", "r"}, []string{"INT4", "TEXT", "INT8", "TEXT"}, rows)
}
