package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func queryParallelScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			body(t, p, b, c)
		})
	}
}

func queryParallelRun(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
}

func queryParallelSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	queryParallelRun(t, p, b, c, fixture.Exec("CREATE TABLE parallel_rows(id INT NOT NULL,k INT,p TEXT)", 0))
	queryParallelRun(t, p, b, c, fixture.Exec("INSERT INTO parallel_rows VALUES(1,2,'a'),(2,NULL,'b'),(3,1,'c'),(4,2,'a'),(5,NULL,'b'),(6,1,NULL)", 6))
}

func queryParallelBag(query string, names, types []string, rows [][]any) fixture.Step {
	s := fixture.Select(query, names, types, rows)
	s.Bag = true
	return s
}

func queryParallelWorkers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, workers string) {
	t.Helper()
	for _, command := range []string{"SET parallel_setup_cost=0", "SET parallel_tuple_cost=0", "SET min_parallel_table_scan_size=0"} {
		queryParallelRun(t, p, b, c, fixture.Exec(command, 0))
	}
	queryParallelRun(t, p, b, c, fixture.Exec("SET max_parallel_workers_per_gather="+workers, 0))
}

func queryParallelDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

// PostgreSQL may report execution errors at Rows.Err after a streamed prefix;
// native eager execution must expose neither a cursor nor a successful result.
func queryParallelFailure(t *testing.T, ctx context.Context, b fixture.Backend, x fixture.Executor, query string, want fixture.Diagnostic) {
	t.Helper()
	rows, err := x.QueryContext(ctx, query)
	if rows != nil {
		defer rows.Close()
		if !b.PG {
			t.Fatal("native failed query published a cursor")
		}
		for rows.Next() {
			// Drain a legitimate streaming prefix without imposing native timing.
		}
		err = rows.Err()
	}
	fixture.AssertDiagnostic(t, err, &want)
	if rows != nil {
		rows.Close()
	}
	result, err := x.ExecContext(ctx, query)
	if result != nil {
		t.Fatal("failed Exec published a result")
	}
	fixture.AssertDiagnostic(t, err, &want)
}
