package reference_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"stuntgres/reference/fixture"
)

// Both modes populate independent source SQL tables. Foreign names deliberately
// differ from quoted source names, including column mappings: a provider must
// consume bound occurrence/attribute metadata, not forward the consumer's SQL.
func fdwPushdownLive(t *testing.T, p *fixture.Pair, b fixture.Backend, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	r.mu.Lock()
	r.queryPushdown = true
	r.mu.Unlock()
	fdwLive(t, p, b, r, run)
	fdwSourceExec(t, p, b, r, run, `CREATE TABLE fdw_source."Left input"("Key" integer,"Value" integer)`, 0)
	fdwSourceExec(t, p, b, r, run, `CREATE TABLE fdw_source."Right input"("Key" integer,"Weight" integer)`, 0)
	fdwSourceExec(t, p, b, r, run, `INSERT INTO fdw_source."Left input" VALUES(1,10),(1,NULL),(2,20),(NULL,30),(4,40)`, 5)
	fdwSourceExec(t, p, b, r, run, `INSERT INTO fdw_source."Right input" VALUES(1,100),(1,100),(3,300),(NULL,900)`, 4)
	run(fixture.Exec(`CREATE FOREIGN TABLE fdw_push_left(k integer OPTIONS(column_name 'Key'),v integer OPTIONS(column_name 'Value')) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'Left input')`, 0))
	run(fixture.Exec(`CREATE FOREIGN TABLE fdw_push_right(k integer OPTIONS(column_name 'Key'),w integer OPTIONS(column_name 'Weight')) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'Right input')`, 0))
}

// Each expectation is asserted independently through both public execution
// routes. Bag comparison preserves duplicates without assuming remote peer order.
func fdwPushdownValue(run func(fixture.Step), query string, columns, types []string, rows [][]any, bag bool, args ...any) {
	s := fixture.Select(query, columns, types, rows, args...)
	s.Bag = bag
	run(s)
	run(fixture.Prepared(s))
}

// SQL output is the primary assertion. These supplemental observations prevent
// a base-scan-only implementation from claiming actual composite execution.
// Hook names follow the optional provider contract; no production API is needed
// to write the initial regression before that contract exists.
func fdwPushdownObserve(r *fdwSQLReader) func(*testing.T, bool, string) {
	var mu sync.Mutex
	calls := make(map[string]int)
	r.setHook(func(ctx context.Context, method string) error {
		mu.Lock()
		calls[method]++
		mu.Unlock()
		return ctx.Err()
	})
	return func(t *testing.T, pg bool, discovery string) {
		t.Helper()
		r.setHook(nil)
		if pg {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if calls[discovery] == 0 || calls["BeginForeignQueryScan"] == 0 {
			t.Errorf("public results were not backed by actual optional composite execution: %s=%d BeginForeignQueryScan=%d", discovery, calls[discovery], calls["BeginForeignQueryScan"])
		}
	}
}

func fdwPushdownHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, steps ...fixture.Step) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stmt.Close(); err != nil {
			t.Error(err)
		}
	}()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	for _, step := range steps {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
	}
}
