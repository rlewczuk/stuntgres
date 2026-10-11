package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func updateFromReference(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
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
			body(t, p, backend, conn)
		})
	}
}

func updateFromRun(t *testing.T, p *fixture.Pair, b fixture.Backend, conn fixture.Executor) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
}

func updateFromSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE t(id INT,v INT,prev INT)", 0))
	run(fixture.Exec("CREATE TABLE s(k INT,n INT)", 0))
	run(fixture.Exec("INSERT INTO t VALUES(1,10,0),(2,20,0),(3,30,0),(4,40,0)", 4))
	run(fixture.Exec("INSERT INTO s VALUES(1,101),(3,303),(9,909),(NULL,707)", 4))
}

func updateFromTargets(rows [][]any) fixture.Step {
	return fixture.Select("SELECT id,v,prev FROM t ORDER BY id", []string{"id", "v", "prev"}, []string{"INT4", "INT4", "INT4"}, rows)
}

func updateFromOriginal() [][]any {
	return [][]any{{int64(1), int64(10), int64(0)}, {int64(2), int64(20), int64(0)}, {int64(3), int64(30), int64(0)}, {int64(4), int64(40), int64(0)}}
}

// Assert the independently recorded complete PostgreSQL diagnostic contract.
func updateFromDiagnostic(t *testing.T, ctx context.Context, conn *sql.Conn, query string, want fixture.Diagnostic, prepared bool) {
	t.Helper()
	var err error
	if prepared {
		var stmt *sql.Stmt
		stmt, err = conn.PrepareContext(ctx, query)
		if stmt != nil {
			stmt.Close()
		}
	} else {
		_, err = conn.ExecContext(ctx, query)
	}
	if err == nil {
		t.Fatalf("expected diagnostic for %s", query)
	}
	fixture.AssertDiagnostic(t, err, &want)
}

func updateFromPosition(query, token string) string {
	i := strings.Index(query, token)
	if i < 0 {
		panic("diagnostic token not present")
	}
	return fixture.Position(utf8.RuneCountInString(query[:i]) + 1)
}
