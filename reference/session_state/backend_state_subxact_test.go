package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBackendStateSubxact(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		id := stateMapped(t, p, b, o, peer)[0]
		activityExec(t, p, peer, "CREATE TABLE bs_children(v integer)")
		activityExec(t, p, peer, "BEGIN")
		defer peer.ExecContext(context.Background(), "ROLLBACK")
		activityExec(t, p, peer, "SAVEPOINT parent")
		activityExec(t, p, peer, "SAVEPOINT child")
		stateSub(t, p, b, o, id, 0, false, true)
		// This asks only for the top XID; lazy marks must remain unassigned.
		var xid string
		if err := peer.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&xid); err != nil || xid == "" {
			t.Fatal(xid, err)
		}
		stateSub(t, p, b, o, id, 0, false, true)
		activityExec(t, p, peer, "INSERT INTO bs_children VALUES(1)")
		stateSub(t, p, b, o, id, 2, false, true)
		activityExec(t, p, o, "BEGIN")
		defer o.ExecContext(context.Background(), "ROLLBACK")
		stateSub(t, p, b, o, id, 2, false, true)
		activityExec(t, p, peer, "RELEASE child")
		stateSub(t, p, b, o, id, 2, false, false)
		activityExec(t, p, peer, "ROLLBACK TO parent")
		stateSub(t, p, b, o, id, 2, false, false)
		stateSub(t, p, b, o, id, 0, false, true)
		activityExec(t, p, peer, "INSERT INTO bs_children VALUES(2)")
		stateSub(t, p, b, o, id, 1, false, true)
		activityExec(t, p, peer, "RELEASE parent")
		stateSub(t, p, b, o, id, 1, false, true)
		activityExec(t, p, peer, "COMMIT")
		stateSub(t, p, b, o, id, 1, false, false)
		stateSub(t, p, b, o, id, 0, false, true)
		activityExec(t, p, o, "COMMIT")
		// The enclosing read-only mark owns every assigned descendant, but
		// contributes no XID itself until a child really writes.
		activityExec(t, p, peer, "BEGIN")
		activityExec(t, p, peer, "SAVEPOINT enclosing")
		for i := 1; i <= 63; i++ {
			activityExec(t, p, peer, fmt.Sprintf("SAVEPOINT s%d", i))
			activityExec(t, p, peer, fmt.Sprintf("INSERT INTO bs_children VALUES(%d)", i+100))
			activityExec(t, p, peer, fmt.Sprintf("RELEASE s%d", i))
		}
		// enclosing + 63 writing siblings fill precisely64 cache positions.
		stateSub(t, p, b, o, id, 64, false, true)
		activityExec(t, p, peer, "SAVEPOINT uncached")
		activityExec(t, p, peer, "INSERT INTO bs_children VALUES(999)")
		stateSub(t, p, b, o, id, 64, true, true)
		activityExec(t, p, peer, "ROLLBACK TO uncached")
		stateSub(t, p, b, o, id, 64, true, true)
		activityExec(t, p, peer, "ROLLBACK TO enclosing")
		stateSub(t, p, b, o, id, 0, true, true)
		activityExec(t, p, peer, "INSERT INTO bs_children VALUES(1000)")
		stateSub(t, p, b, o, id, 1, true, true)
		activityExec(t, p, peer, "COMMIT")
		stateSub(t, p, b, o, id, 0, false, true)
		for _, end := range []string{"ROLLBACK", "abort"} {
			activityExec(t, p, peer, "BEGIN")
			activityExec(t, p, peer, "SAVEPOINT writing")
			activityExec(t, p, peer, "INSERT INTO bs_children VALUES(2000)")
			stateSub(t, p, b, o, id, 1, false, true)
			if end == "abort" {
				activityExec(t, p, peer, "RELEASE writing")
				_, err := peer.ExecContext(p.Ctx, "SELECT 1/0")
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
				stateSub(t, p, b, o, id, 0, false, true)
			}
			activityExec(t, p, peer, "ROLLBACK")
			stateSub(t, p, b, o, id, 0, false, true)
		}
		if n := backendCount(t, p, peer, "SELECT count(*) FROM bs_children"); n != 2 {
			t.Fatal("real subtree rollback/commit effects", n)
		}
	})
}
