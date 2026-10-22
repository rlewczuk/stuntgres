package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLoadPrepared(t *testing.T) {
	loadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		loadRole(t, p, b, c, "load_prepared_super", "SUPERUSER")
		loadRole(t, p, b, c, "load_prepared_reader", "INHERIT")
		loadExec(t, p, b, c, "GRANT load_prepared_super TO load_prepared_reader WITH INHERIT TRUE, SET TRUE")
		loadExec(t, p, b, c, "SET ROLE load_prepared_reader")
		held := loadHeld(t, p, c, loadSQL(loadMissing))
		loadHeldError(t, p, b, held, loadDenied(loadMissing))
		loadExec(t, p, b, c, "RESET ROLE")
		loadHeldError(t, p, b, held, loadBoundary(b, loadMissing))
		loadExec(t, p, b, c, "SET ROLE load_prepared_reader")
		loadHeldError(t, p, b, held, loadDenied(loadMissing))

		// Ordinary SESSION_USER with actual superuser CURRENT_USER: neither
		// authenticated identity nor inherited SUPERUSER determines authority.
		loadExec(t, p, b, c, "RESET ROLE")
		loadExec(t, p, b, c, "SET SESSION AUTHORIZATION load_prepared_reader")
		loadExec(t, p, b, c, "SET ROLE load_prepared_super")
		loadHeldError(t, p, b, held, loadBoundary(b, loadMissing))
		live := loadHeld(t, p, c, loadSQL(loadMissing))
		loadExec(t, p, b, b.DB, "ALTER ROLE load_prepared_super NOSUPERUSER")
		loadHeldError(t, p, b, live, loadDenied(loadMissing))
		loadExec(t, p, b, b.DB, "ALTER ROLE load_prepared_super SUPERUSER")
		loadHeldError(t, p, b, live, loadBoundary(b, loadMissing))
	})
}

func TestReferenceLoadTransactions(t *testing.T) {
	loadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		loadExec(t, p, b, c, "CREATE TABLE load_sentinel(id INT)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO load_sentinel VALUES(7)", 1))
		loadRole(t, p, b, c, "load_transaction_reader", "")
		before := loadInventory(t, p, c)
		for _, begin := range []string{"BEGIN", "BEGIN READ ONLY"} {
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					filename string
					reader   bool
					want     fixture.Diagnostic
				}{
					{loadMissing, true, loadDenied(loadMissing)},
					{"$bad/missing", false, loadDiagnostic("42602", "invalid macro name in path: $bad/missing")},
					{loadPlugin, false, loadBoundary(b, loadPlugin)},
				} {
					t.Run(fmt.Sprintf("%s/prepared=%t/%s", begin, prepared, tc.filename), func(t *testing.T) {
						defer c.ExecContext(p.Ctx, "RESET ROLE")
						active := false
						defer func() {
							if active {
								c.ExecContext(p.Ctx, "ROLLBACK")
							}
						}()
						if tc.reader {
							loadExec(t, p, b, c, "SET ROLE load_transaction_reader")
						}
						loadExec(t, p, b, c, begin)
						active = true
						loadExec(t, p, b, c, "SAVEPOINT load_recover")
						s := fixture.ErrorDiagnostic(loadSQL(tc.filename), tc.want)
						s.Prepared, s.ErrorStage = prepared, "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						aborted := fixture.ErrorDiagnostic("SELECT 1", loadDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
						aborted.SelectRows, aborted.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, aborted)
						loadExec(t, p, b, c, "ROLLBACK TO SAVEPOINT load_recover")
						loadExec(t, p, b, c, "RESET ROLE")
						loadSentinel(t, p, b, c)
						loadExec(t, p, b, c, "COMMIT")
						active = false
						loadExec(t, p, b, c, "RESET ROLE")
						loadSentinel(t, p, b, b.DB)
					})
				}
			}
		}
		loadCanceled(t, p, b, c)
		loadSentinel(t, p, b, c)
		if after := loadInventory(t, p, c); !reflect.DeepEqual(before, after) {
			t.Fatalf("LOAD changed installed extension identities/rows: before=%#v after=%#v", before, after)
		}
	})
}

func loadCanceled(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	// Pre-canceled pq Prepare can invalidate its connection; that adapter
	// lifecycle is not a LOAD semantic. Keep cancellation proof native-only.
	if b.PG {
		return
	}
	query := loadSQL(loadMissing)
	held := loadHeld(t, p, c, query)
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	stmt, err := c.PrepareContext(ctx, query)
	if stmt != nil {
		defer stmt.Close()
	}
	if stmt != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("native LOAD Prepare cancellation: handle=%v error=%v", stmt, err)
	}
	for _, e := range []fixture.Executor{c, held} {
		result, err := e.ExecContext(ctx, query)
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("LOAD cancellation result=%v error=%v", result, err)
		}
	}
	loadHeldError(t, p, b, held, loadBoundary(b, loadMissing))
}

// Snapshot the public installed inventory, not internal allocators or wiring.
func loadInventory(t *testing.T, p *fixture.Pair, c fixture.Executor) [][6]string {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT oid,extname,extowner,extnamespace,extrelocatable,extversion FROM pg_catalog.pg_extension ORDER BY extname")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var inventory [][6]string
	for rows.Next() {
		var row [6]string
		if err := rows.Scan(&row[0], &row[1], &row[2], &row[3], &row[4], &row[5]); err != nil {
			t.Fatal(err)
		}
		inventory = append(inventory, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return inventory
}

func TestReferenceLoadFeatureSmoke(t *testing.T) {
	loadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		loadExec(t, p, b, c, "CREATE TABLE load_sentinel(id INT)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO load_sentinel VALUES(7)", 1))
		before := loadInventory(t, p, c)
		loadRole(t, p, b, c, "load_smoke_reader", "")
		loadExec(t, p, b, c, "SET ROLE load_smoke_reader")
		loadError(t, p, b, c, loadSQL(loadMissing), loadDenied(loadMissing), false)
		loadError(t, p, b, c, loadSQL(loadPlugin), loadBoundary(b, loadPlugin), false)
		held := loadHeld(t, p, c, loadSQL(loadMissing))
		loadHeldError(t, p, b, held, loadDenied(loadMissing))
		loadExec(t, p, b, c, "RESET ROLE")
		loadHeldError(t, p, b, held, loadBoundary(b, loadMissing))
		loadError(t, p, b, c, "LOAD '$bad/missing'", loadDiagnostic("42602", "invalid macro name in path: $bad/missing"), false)
		loadExec(t, p, b, c, "BEGIN READ ONLY")
		loadExec(t, p, b, c, "SAVEPOINT load_recover")
		loadHeldError(t, p, b, held, loadBoundary(b, loadMissing))
		loadExec(t, p, b, c, "ROLLBACK TO SAVEPOINT load_recover")
		loadSentinel(t, p, b, c)
		loadExec(t, p, b, c, "COMMIT")
		loadSentinel(t, p, b, b.DB)
		if after := loadInventory(t, p, c); !reflect.DeepEqual(before, after) {
			t.Fatalf("LOAD changed installed extension identities/rows: before=%#v after=%#v", before, after)
		}
	})
}
