package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwRowLockAdmissionBoundaries(t *testing.T) {
	fdwRowLockConfiguredScenario(t, false, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, locker *fdwSQLRowLocker, run func(fixture.Step)) {
		for _, prepared := range []bool{false, true} {
			q := "SELECT id,label,flag FROM fdw_data f FOR UPDATE OF missing"
			want := fdwDiagnostic("42P01", `relation "missing" in FOR UPDATE clause not found in FROM clause`)
			want.Position = strconv.Itoa(strings.LastIndex(q, "missing") + 1)
			s := fixture.ErrorDiagnostic(q, want)
			s.Prepared = prepared
			run(s)
		}
		// Holding the real source row makes accidental prepare-time locking
		// observable as a blocked Prepare, rather than relying on counters.
		holder, _ := fixture.LockConnections(t, p.Ctx, source)
		fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, holder, "UPDATE fdw_source.data SET label=label WHERE rid=1", 1)
		before := locker.transactions
		stmt, err := c.PrepareContext(p.Ctx, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE")
		if err != nil {
			t.Fatal(err)
		}
		if !b.PG && locker.transactions != before {
			t.Fatal("Prepare enlisted a runtime transaction")
		}
		fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
		for range 2 {
			rows, err := stmt.QueryContext(p.Ctx)
			fdwRowLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, [][]any{{int64(1), "one", true}})
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		// Aliases are separate occurrences; repeated identities must not erase
		// result multiplicity even though only occurrence a is marked.
		for _, prepared := range []bool{false, true} {
			fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT a.id,a.label,a.flag FROM fdw_data a CROSS JOIN fdw_data b ORDER BY a.id,b.id FOR UPDATE OF a", prepared, [][]any{{int64(1), "one", true}, {int64(1), "one", true}, {int64(2), "two", false}, {int64(2), "two", false}}))
		}
		run(fixture.Exec("CREATE TABLE fdw_projection_calls(id integer)", 0))
		run(fixture.Exec("CREATE FUNCTION fdw_row_lock_label(value text) RETURNS text LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO fdw_projection_calls VALUES(1); RETURN value; END$$", 0))
		projected := "SELECT id,fdw_row_lock_label(label) AS label,flag FROM fdw_data WHERE id=1 FOR UPDATE"
		fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, projected, false, [][]any{{int64(1), "one", true}}))
		run(fixture.Select("SELECT count(*) AS n FROM fdw_projection_calls", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		cached, err := c.PrepareContext(p.Ctx, projected)
		if err != nil {
			t.Fatal(err)
		}
		for n := int64(2); n <= 3; n++ {
			rows, err := cached.QueryContext(p.Ctx)
			fdwRowLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, [][]any{{int64(1), "one", true}})
			run(fixture.Select("SELECT count(*) AS n FROM fdw_projection_calls", []string{"n"}, []string{"INT8"}, [][]any{{n}}))
		}
		if err := cached.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("CREATE ROLE fdw_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_reader", 0))
		run(fixture.Exec("GRANT SELECT ON fdw_data TO fdw_reader", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT id,label,flag FROM fdw_data FOR UPDATE", fdwDiagnostic("42501", "permission denied for foreign table fdw_data"))
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("BEGIN READ ONLY", 0))
		for _, prepared := range []bool{false, true} {
			// Each error aborts its owner; recover with a real savepoint.
			run(fixture.Exec("SAVEPOINT admission", 0))
			s := fixture.ErrorDiagnostic("SELECT id,label,flag FROM fdw_data FOR UPDATE", fdwDiagnostic("25006", "cannot execute SELECT FOR UPDATE in a read-only transaction"))
			s.Prepared = prepared
			run(s)
			run(fixture.Exec("ROLLBACK TO admission", 0))
		}
		run(fixture.Exec("ROLLBACK", 0))
		if !b.PG {
			// The deliberately scan-only wrapper retains default COPY semantics:
			// visible rows are not a claim to physical remote row locks.
			run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_scan_only_handler", 0))
			run(fixture.Exec("BEGIN", 0))
			fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE", true, [][]any{{int64(1), "one", true}}))
			fixture.LockExec(t, p.Ctx, source, "UPDATE fdw_source.data SET label='copy-unlocked',revision=2 WHERE rid=1", 1)
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_sql_handler", 0))
		}
	})
}

var _ stuntgres.ForeignRowMarkProvider = (*fdwSQLRowLocker)(nil)
var _ stuntgres.ForeignRowRefetcher = (*fdwSQLRowLocker)(nil)
var _ stuntgres.ForeignScanRechecker = (*fdwSQLRowLocker)(nil)
