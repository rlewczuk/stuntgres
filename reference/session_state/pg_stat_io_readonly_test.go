package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func statIOMutationDiagnostic(operation string) fixture.Diagnostic {
	action := map[string]string{"INSERT":"inserting into", "UPDATE":"updating", "DELETE":"deleting from"}[operation]
	message := map[string]string{"INSERT":`cannot insert into view "pg_stat_io"`, "UPDATE":`cannot update view "pg_stat_io"`, "DELETE":`cannot delete from view "pg_stat_io"`}[operation]
	return fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"55000", Message:message, Detail:"Views that do not select from a single table or view are not automatically updatable.", Hint:"To enable "+action+" the view, provide an INSTEAD OF "+operation+" trigger or an unconditional ON "+operation+" DO INSTEAD rule."}
}

func TestReferencePGStatIOPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"io_public_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		activityExec(t, p, c, "CREATE ROLE io_public_reader")
		defer c.ExecContext(context.Background(), "RESET ROLE")
		first := statIOResetAfter(t, p, b, c, "pg_stat_reset_shared('io')", time.Time{})
		for _, role := range []string{"io_public_reader", "pg_read_all_stats"} {
			activityExec(t, p, c, "SET ROLE "+role)
			for _, prepared := range []bool{false, true} {
				var e fixture.Executor = c
				if prepared {
					stmt, err := c.PrepareContext(p.Ctx, statIOSelect)
					if err != nil { t.Fatal(err) }
					defer stmt.Close()
					e = fixture.HeldStatement{Executor:c, Statement:stmt, SQL:statIOSelect}
				}
				got := statIOQuery(t, p, b, e, statIOSelect)
				statIOCheck(t, got, b.PG)
				if !statIOStamp(t, got).Equal(first) { t.Fatal("public reset metadata redacted", role) }
			}
			resetReplicationRuntimeError(t, p, b, c, "SELECT pg_stat_reset_shared('io')", activityDiagnostic("42501", "permission denied for function pg_stat_reset_shared", 0))
			activityExec(t, p, c, "RESET ROLE")
			if !statIOStamp(t, statIOFresh(t, p, b, c)).Equal(first) { t.Fatal("denied reset changed state") }
		}
		identity := "pg_catalog.pg_stat_reset_shared(text)"
		activityExec(t, p, c, "GRANT EXECUTE ON FUNCTION "+identity+" TO io_public_reader")
		defer func() {
			c.ExecContext(context.Background(), "RESET ROLE")
			c.ExecContext(context.Background(), "REVOKE EXECUTE ON FUNCTION "+identity+" FROM io_public_reader")
		}()
		activityExec(t, p, c, "SET ROLE io_public_reader")
		if !statIOResetAfter(t, p, b, c, "pg_stat_reset_shared('io')", first).After(first) { t.Fatal("granted reset did not publish") }
		activityExec(t, p, c, "RESET ROLE")
	})
}

func TestReferencePGStatIOReadOnly(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		activityExec(t, p, c, "CREATE TABLE io_readonly_sentinel(v integer)")
		activityExec(t, p, c, "INSERT INTO io_readonly_sentinel VALUES(37)")
		before := statIOFresh(t, p, b, c)
		stmt, err := c.PrepareContext(p.Ctx, statIOSelect)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor:c, Statement:stmt, SQL:statIOSelect}
		for _, tc := range []struct{ query, operation string }{
			{"INSERT INTO pg_catalog.pg_stat_io(backend_type) VALUES('x')", "INSERT"},
			{"UPDATE pg_catalog.pg_stat_io SET backend_type='x' WHERE backend_type='client backend'", "UPDATE"},
			{"DELETE FROM pg_catalog.pg_stat_io WHERE backend_type='client backend'", "DELETE"},
			{"UPDATE pg_catalog.pg_stat_io SET backend_type='x' WHERE false", "UPDATE"},
			{"DELETE FROM pg_catalog.pg_stat_io WHERE false", "DELETE"},
			{"TRUNCATE pg_catalog.pg_stat_io", "TRUNCATE"},
		} {
			for _, prepared := range []bool{false, true} {
				want := statIOMutationDiagnostic(tc.operation)
				if tc.operation=="TRUNCATE" { want=activityDiagnostic("42809", `"pg_stat_io" is not a table`, 0) }
				step := fixture.ErrorDiagnostic(tc.query, want)
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared && tc.operation!="TRUNCATE" { step.ErrorStage="prepare" }
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				statIOClear(t, p, b, c)
				after := statIOQuery(t, p, b, held, statIOSelect)
				statIOCheck(t, after, b.PG)
				if !b.PG && !reflect.DeepEqual(before, after) { t.Fatal("mutation changed IO state") }
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM io_readonly_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
			}
		}
		pool := p.Native
		if b.PG { pool=p.CopyPostgres }
		copyConn, err := pool.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer copyConn.Close()
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_io(backend_type) FROM STDIN", strings.NewReader("x\n"))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42809", Message:`cannot copy to view "pg_stat_io"`, Hint:"To enable copying to a view, provide an INSTEAD OF INSERT trigger."})
		if result.Count!=0 || result.CommandTag!="" { t.Fatal("COPY published success", result) }
		const cancelQuery = "SELECT io.backend_type FROM pg_catalog.pg_stat_io AS io CROSS JOIN io_readonly_sentinel AS sentinel WHERE sentinel.v=37"
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			t.Run(route.name, func(t *testing.T) {
				// A canceled pq query can close its transport; return this borrower
				// before the next route rather than reusing the interrupted session.
				cancelConn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer cancelConn.Close()
				var e fixture.Executor = cancelConn
				if route.prepared {
					cancelStmt, err := cancelConn.PrepareContext(p.Ctx, cancelQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer cancelStmt.Close()
					e = fixture.HeldStatement{Executor:cancelConn, Statement:cancelStmt, SQL:cancelQuery}
				}
				// Prepare before taking the conflicting lock: PostgreSQL preparation
				// itself needs AccessShareLock on the sentinel. Keep the actual read
				// blocked so even pq's prepared route cannot publish a fast row before
				// its context-cancellation watcher runs.
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				tx, err := peer.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE io_readonly_sentinel IN ACCESS EXCLUSIVE MODE", 0)
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				rows, err := e.QueryContext(ctx, cancelQuery)
				if rows != nil {
					defer rows.Close()
					// database/sql may defer the cancellation error until iteration.
					if rows.Next() {
						t.Fatal("canceled query published a successful row")
					}
					if err == nil {
						err = rows.Err()
					}
				}
				if b.PG {
					// pq exposes PostgreSQL's cancellation diagnostic for the
					// blocked read rather than wrapping the Go context error.
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{
						Severity:            "ERROR",
						SeverityUnlocalized: "ERROR",
						Code:                "57014",
						Message:             "canceling statement due to user request",
					})
				} else if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary context boundary", err)
				}
			})
		}
		after := statIOFresh(t, p, b, c)
		if !b.PG && !reflect.DeepEqual(before, after) { t.Fatal("COPY/cancellation changed IO") }
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM io_readonly_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
	})
}

func TestReferencePGStatIOFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"io_smoke_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer peer.Close()
		statIOCheck(t, statIOQuery(t, p, b, c, statIOSelect), b.PG)
		query := "SELECT * FROM pg_stat_io WHERE backend_type=$1 AND object=$2 AND context=$3"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor:c, Statement:stmt, SQL:query}
		rows := statIOQuery(t, p, b, held, query, "client backend", "temp relation", "normal")
		if len(rows)!=1 { t.Fatal("prepared capability", rows) }
		for _, i := range []int{9,10,16,17,18} { if rows[0][i]!=nil { t.Fatal("unsupported temp operation", statIONames[i], rows[0][i]) } }
		for _, i := range []int{3,4,5,6,7,8,11,12,13,14,15} { if rows[0][i]==nil { t.Fatal("supported temp operation masked", statIONames[i]) } }
		if !b.PG && (rows[0][3]!=int64(0) || rows[0][4]!="0" || rows[0][5]!=float64(0)) { t.Fatal("typed absent physical estimates", rows) }
		first := statIOResetAfter(t, p, b, peer, "pg_stat_reset_shared('io')", time.Time{})
		later := statIOResetAfter(t, p, b, peer, "pg_stat_reset_shared('io')", first)
		if !statIOStamp(t, statIOFresh(t, p, b, c)).Equal(later) { t.Fatal("IO reset not visible across all objects") }
		resetReplicationVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('wal')", "pg_stat_reset_shared")
		if !statIOStamp(t, statIOFresh(t, p, b, c)).Equal(later) { t.Fatal("WAL reset changed IO") }
		activityExec(t, p, c, "CREATE ROLE io_smoke_reader")
		activityExec(t, p, c, "SET ROLE io_smoke_reader")
		defer c.ExecContext(context.Background(), "RESET ROLE")
		statIOCheck(t, statIOQuery(t, p, b, c, statIOSelect), b.PG)
		activityExec(t, p, c, "RESET ROLE")
		activityExec(t, p, c, "CREATE TABLE io_smoke_sentinel(v integer)")
		activityExec(t, p, c, "INSERT INTO io_smoke_sentinel VALUES(37)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("UPDATE pg_stat_io SET backend_type='x' WHERE false", statIOMutationDiagnostic("UPDATE")))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM io_smoke_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
	})
}
