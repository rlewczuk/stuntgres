package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func sqlCursorDemand(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	sqlCursorStep(t, p, b, c, fixture.Exec("CREATE SEQUENCE cursor_draw START 10", 0))
	sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
	sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE lazy NO SCROLL CURSOR FOR SELECT i,nextval('cursor_draw') AS n FROM cursor_rows ORDER BY i", 0))
	state := func(n int64, called bool) {
		sqlCursorStep(t, p, b, c, fixture.Select("SELECT last_value,is_called FROM cursor_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}}))
	}
	state(10, false)
	sqlCursorStep(t, p, b, c, fixture.Select("FETCH 1 FROM lazy", []string{"i", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}}))
	state(10, true)
	sqlCursorStep(t, p, b, c, fixture.Select("FETCH 1 FROM lazy", []string{"i", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(11)}}))
	state(11, true)
	sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE lazy", 0))
	state(11, true)
	sqlCursorStep(t, p, b, c, fixture.Exec("COMMIT", 0))
}

func TestReferenceSQLCursorDeclarationTiming(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		sqlCursorDemand(t, p, b, c)
		sqlCursorStep(t, p, b, c, fixture.Exec("CREATE TABLE cursor_div(i INTEGER)", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO cursor_div VALUES(0),(2)", 2))
		sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
		// OFFSET 0 keeps the ordered source boundary; arithmetic is a late outer target.
		// Pinned discovery must confirm first5 then22012 rather than planning-time evaluation.
		sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE failing NO SCROLL CURSOR FOR SELECT 10/i AS n FROM (SELECT i FROM cursor_div ORDER BY i DESC OFFSET 0) AS ordered", 0))
		sqlCursorStep(t, p, b, c, fixture.Select("FETCH 1 FROM failing", []string{"n"}, []string{"INT4"}, [][]any{{int64(5)}}))
		sqlCursorError(t, p, b, c, "FETCH 1 FROM failing", sqlCursorDiagnostic("22012", "division by zero"))
		sqlCursorError(t, p, b, c, "FETCH 1 FROM failing", sqlCursorDiagnostic("55000", "portal \"failing\" cannot be run"))
		sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE failing", 0))
	})
}

func TestReferenceSQLCursorSnapshots(t *testing.T) {
	for _, peer := range []bool{false, true} {
		name := "own"
		if peer {
			name = "peer"
		}
		t.Run(name, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
				if !peer {
					sqlCursorStep(t, p, b, c, fixture.Exec("UPDATE cursor_rows SET t='preexisting' WHERE i=1", 1))
				}
				sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE snap NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
				var writer fixture.Executor = c
				if peer {
					other, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer other.Close()
					writer = other
				}
				sqlCursorStep(t, p, b, writer, fixture.Exec("UPDATE cursor_rows SET t='changed' WHERE i=1", 1))
				sqlCursorStep(t, p, b, writer, fixture.Exec("INSERT INTO cursor_rows VALUES(4,'four')", 1))
				sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE newer NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
				first := "one"
				if !peer {
					first = "preexisting"
				}
				sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH ALL FROM snap", [][]any{{int64(1), first}, {int64(2), nil}, {int64(3), "three"}}, b.PG))
				current := [][]any{{int64(1), "changed"}, {int64(2), nil}, {int64(3), "three"}, {int64(4), "four"}}
				sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH ALL FROM newer", current, b.PG))
				sqlCursorStep(t, p, b, c, fixture.Select("SELECT i,t FROM cursor_rows ORDER BY i", []string{"i", "t"}, []string{"INT4", "TEXT"}, current))
			})
		})
	}
}

func TestReferenceSQLCursorZeroAndDirections(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
		decl := "DECLARE z NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i"
		sqlCursorStep(t, p, b, c, fixture.Exec(decl, 0))
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH FORWARD 0 FROM z", [][]any{}, b.PG))
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH FORWARD +1 FROM z", [][]any{{int64(1), "one"}}, b.PG))
		forward := sqlCursorDiagnostic("55000", "cursor can only scan forward")
		forward.Hint = "Declare it with SCROLL option to enable backward scan."
		sqlCursorError(t, p, b, c, "FETCH FORWARD 0 FROM z", forward)
		sqlCursorError(t, p, b, c, "FETCH ALL FROM z", sqlCursorDiagnostic("55000", "portal \"z\" cannot be run"))
		sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE z", 0))
		for _, query := range []string{"FETCH BACKWARD 1 FROM z", "FETCH BACKWARD ALL FROM z", "FETCH FORWARD -1 FROM z"} {
			sqlCursorStep(t, p, b, c, fixture.Exec(decl, 0))
			sqlCursorError(t, p, b, c, query, forward)
			sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE z", 0))
		}
		sqlCursorError(t, p, b, c, "FETCH BACKWARD 1 FROM missing", sqlCursorMissing("missing"))
		sqlCursorStep(t, p, b, c, fixture.Exec(decl, 0))
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH FORWARD 99 FROM z", [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}, b.PG))
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH FORWARD 0 FROM z", [][]any{}, b.PG))
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH ALL FROM z", [][]any{}, b.PG))
		sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE z", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec(decl, 0))
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH FORWARD 3 FROM z", [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}, b.PG))
		sqlCursorError(t, p, b, c, "FETCH FORWARD 0 FROM z", forward)
	})
}

func TestReferenceSQLCursorQueryConsumers(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		sqlCursorStep(t, p, b, c, fixture.Exec("CREATE TABLE cursor_dups(i INTEGER)", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO cursor_dups VALUES(1),(2),(2),(3)", 4))
		sqlCursorStep(t, p, b, c, fixture.Exec("CREATE VIEW cursor_view AS SELECT i,t FROM cursor_rows", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE consumers NO SCROLL CURSOR FOR SELECT v.i,v.t FROM (SELECT i,t FROM cursor_view WHERE i >= 2) AS v JOIN cursor_dups AS d ON v.i=d.i ORDER BY v.i LIMIT 3", 0))
		// Stored view columns retain their declared nullable metadata through
		// the derived/join consumer, independently of the base table's NOT NULL.
		sqlCursorStep(t, p, b, c, sqlCursorBatchMetadata("FETCH 1 FROM consumers", [][]any{{int64(2), nil}}, b.PG, []bool{true, true}))
		sqlCursorStep(t, p, b, c, sqlCursorBatchMetadata("FETCH ALL FROM consumers", [][]any{{int64(2), nil}, {int64(3), "three"}}, b.PG, []bool{true, true}))
		sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE locked NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows WHERE i=1 FOR UPDATE", 0))
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM locked", [][]any{{int64(1), "one"}}, b.PG))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer peer.ExecContext(p.Ctx, "ROLLBACK")
		sqlCursorStep(t, p, b, peer, fixture.Exec("BEGIN", 0))
		sqlCursorStep(t, p, b, peer, fixture.ErrorDiagnostic("SELECT i FROM cursor_rows WHERE i=1 FOR UPDATE NOWAIT", sqlCursorDiagnostic("55P03", "could not obtain lock on row in relation \"cursor_rows\"")))
		sqlCursorStep(t, p, b, peer, fixture.Exec("ROLLBACK", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE locked", 0))
		// Closing the cursor does not release transaction-owned row locks.
		sqlCursorStep(t, p, b, peer, fixture.Exec("BEGIN", 0))
		sqlCursorStep(t, p, b, peer, fixture.ErrorDiagnostic("SELECT i FROM cursor_rows WHERE i=1 FOR UPDATE NOWAIT", sqlCursorDiagnostic("55P03", "could not obtain lock on row in relation \"cursor_rows\"")))
		sqlCursorStep(t, p, b, peer, fixture.Exec("ROLLBACK", 0))
		sqlCursorStep(t, p, b, c, fixture.Exec("COMMIT", 0))
		sqlCursorStep(t, p, b, peer, fixture.Select("SELECT i FROM cursor_rows WHERE i=1 FOR UPDATE NOWAIT", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
