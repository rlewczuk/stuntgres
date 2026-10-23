package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWParallelConversion(t *testing.T) {
	fdwParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwParallelReader, run func(fixture.Step)) {
		fdwParallelLive(t, p, b, r, run)
		run(fixture.Exec("SET max_parallel_workers_per_gather=2", 0))
		fdwSourceExec(t, p, b, r.fdwSQLReader, run, "CREATE TABLE fdw_source.convert(payload text)", 0)
		fdwSourceExec(t, p, b, r.fdwSQLReader, run, "INSERT INTO fdw_source.convert VALUES('nope'),('nope')", 2)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_conversion(payload integer) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'convert')", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_short(payload varchar(3)) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'convert')", 0))
		// These complete diagnostic records are the existing serial conversion
		// contract, including absence of all other exposed fields, not values
		// learned from the parallel implementation.
		for _, tc := range []struct{ value, query, code, message, column string }{
			{"nope", "SELECT p FROM fdw_conversion AS f(p)", "22P02", `invalid input syntax for type integer: "nope"`, "p"},
			{"2147483648", "SELECT payload FROM fdw_conversion f", "22003", `value "2147483648" is out of range for type integer`, "payload"},
			{"abcd", "SELECT payload FROM fdw_short f", "22001", "value too long for type character varying(3)", "payload"},
		} {
			fdwSourceExec(t, p, b, r.fdwSQLReader, run, "UPDATE fdw_source.convert SET payload='"+tc.value+"'", 2)
			for _, prepared := range []bool{false, true} {
				d := fdwDiagnostic(tc.code, tc.message)
				d.Context = `column "` + tc.column + `" of foreign table "f"`
				s := fixture.ErrorDiagnostic(tc.query, d)
				s.Prepared, s.SelectRows = prepared, true
				if b.PG {
					run(s)
				} else {
					fdwParallelConversionFailure(t, p, c, r, tc.query, prepared, d)
				}
				// Recover the same public SQL owner after every failed conversion.
				run(fdwParallelIDs("SELECT id FROM fdw_data ORDER BY id", 1, 2, 3, 4, 5, 6))
			}
		}
	})
}

func fdwParallelConversionFailure(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fdwParallelReader, query string, prepared bool, want fixture.Diagnostic) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	r.partitions = 2
	before := r.parallelCounts()
	sibling := make(chan struct{})
	var entered sync.Once
	var mu sync.Mutex
	waiting, exited, earlyEnd := false, false, false
	r.hook = func(ctx context.Context, phase string, index int) error {
		if phase == "Iterate" && index == 1 {
			mu.Lock()
			waiting = true
			mu.Unlock()
			entered.Do(func() { close(sibling) })
			<-ctx.Done()
			mu.Lock()
			exited = true
			mu.Unlock()
			return ctx.Err()
		}
		if phase == "Iterate" && index == 0 {
			select {
			case <-sibling:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if phase == "End" || phase == "EndShared" {
			mu.Lock()
			defer mu.Unlock()
			if !exited {
				earlyEnd = true
				return fmt.Errorf("conversion cleanup preceded sibling exit")
			}
		}
		return ctx.Err()
	}
	defer func() { r.hook = nil }()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, prepareErr := c.PrepareContext(ctx, query)
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(ctx)
	} else {
		rows, err = c.QueryContext(ctx, query)
	}
	if rows != nil {
		rows.Close()
		t.Fatal("conversion failure published eager Rows")
	}
	fixture.AssertDiagnostic(t, err, &want)
	r.hook = nil
	mu.Lock()
	defer mu.Unlock()
	after := r.parallelCounts()
	if !waiting || !exited || earlyEnd || after.begins-before.begins != 1 || after.opened-before.opened != 2 || after.closed-before.closed != 2 || after.ends-before.ends != 1 {
		t.Fatalf("conversion cleanup: waiting=%v exited=%v earlyEnd=%v before=%+v after=%+v", waiting, exited, earlyEnd, before, after)
	}
	fdwParallelBalanced(t, r)
}
