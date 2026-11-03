package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatsResetSharedControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"shared_control_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		activityExec(t, p, c, "CREATE ROLE shared_control_reader")
		defer c.ExecContext(context.Background(), "RESET ROLE")
		for _, f := range resetSharedFunctions {
			identity := "pg_catalog." + f.name + "(" + f.argument + ")"
			activityExec(t, p, c, "GRANT EXECUTE ON FUNCTION "+identity+" TO shared_control_reader")
			defer func() {
				c.ExecContext(context.Background(), "RESET ROLE")
				c.ExecContext(context.Background(), "REVOKE EXECUTE ON FUNCTION "+identity+" FROM shared_control_reader")
			}()
		}
		var pid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil || pid <= 0 {
			t.Fatal(pid, err)
		}
		for _, f := range resetSharedFunctions {
			argument := "NULL"
			if f.strict {
				argument = "pg_backend_pid()"
			}
			activityExec(t, p, c, "SET ROLE shared_control_reader")
			resetReplicationVoid(t, p, b, c, "SELECT "+f.name+"("+argument+")", f.name)
			activityExec(t, p, c, "RESET ROLE")
		}
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		var peerPID int64
		if err := peer.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&peerPID); err != nil || peerPID <= 0 || peerPID == pid {
			peer.Close()
			t.Fatal("distinct live PID", peerPID, pid, err)
		}
		resetReplicationVoid(t, p, b, c, "SELECT pg_stat_reset_backend_stats($1::integer)", "pg_stat_reset_backend_stats", peerPID)
		if err := peer.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
			peer.Close()
			t.Fatal("physical peer close", err)
		}
		peer.Close()
		resetReplicationVoid(t, p, b, c, "SELECT pg_stat_reset_backend_stats($1::integer)", "pg_stat_reset_backend_stats", peerPID)
		if !b.PG {
			retained, err := c.QueryContext(p.Ctx, "SELECT pg_stat_reset_shared(NULL),pg_stat_reset_backend_stats(NULL)")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			for _, f := range resetSharedFunctions {
				var acl string
				if err := c.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1::oid", f.oid).Scan(&acl); err != nil || acl != "{postgres=X/postgres}" {
					t.Fatal("reset ACL", acl, err)
				}
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			var after int64
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&after); err != nil || after != pid {
				t.Fatal("native control changed live identity", pid, after, err)
			}
			for _, f := range resetSharedFunctions {
				var acl string
				if err := c.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1::oid", f.oid).Scan(&acl); err != nil || acl != "{postgres=X/postgres,shared_control_reader=X/postgres}" {
					t.Fatal("restore ACL", acl, err)
				}
			}
			if !retained.Next() {
				t.Fatal("retained VOID row missing", retained.Err())
			}
			var value string
			var null any
			if err := retained.Scan(&value, &null); err != nil || value != "" || null != nil {
				t.Fatal(value, null, err)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained cardinality", retained.Err())
			}
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector("shared-reset-independent")
			if err != nil {
				t.Fatal(err)
			}
			independent := sql.OpenDB(connector)
			defer independent.Close()
			for _, f := range resetSharedFunctions {
				var acl string
				if err := independent.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1::oid", f.oid).Scan(&acl); err != nil || acl != "{postgres=X/postgres}" {
					t.Fatal("independent ACL", acl, err)
				}
			}
		}
	})
}

func TestReferenceStatsResetSharedFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"shared_smoke_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		activityExec(t, p, c, "CREATE TABLE shared_reset_sentinel(v integer)")
		activityExec(t, p, c, "INSERT INTO shared_reset_sentinel VALUES(7)")
		query := "SELECT pg_stat_reset_shared($1::text)"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		for _, arg := range []any{"wal", nil} {
			resetReplicationVoid(t, p, b, held, query, "pg_stat_reset_shared", arg)
		}
		for _, call := range []struct{ sql, name string }{{"pg_stat_reset_shared()", "pg_stat_reset_shared"}, {"pg_stat_reset_shared(target => 'io')", "pg_stat_reset_shared"}, {"pg_stat_reset_slru()", "pg_stat_reset_slru"}, {"pg_stat_reset_slru('transaction')", "pg_stat_reset_slru"}, {"pg_stat_reset_slru('unknown')", "pg_stat_reset_slru"}, {"pg_stat_reset_backend_stats(pg_backend_pid())", "pg_stat_reset_backend_stats"}} {
			resetReplicationVoid(t, p, b, c, "SELECT "+call.sql, call.name)
		}
		resetSharedNull(t, p, b, c, "SELECT pg_stat_reset_backend_stats(NULL)")
		want := activityDiagnostic("22023", `unrecognized reset target: "WAL"`, 0)
		want.Hint = resetSharedHint
		resetReplicationRuntimeError(t, p, b, c, "SELECT pg_stat_reset_shared('WAL')", want)
		activityExec(t, p, c, "CREATE ROLE shared_smoke_reader")
		identity := "pg_catalog.pg_stat_reset_backend_stats(integer)"
		defer func() {
			c.ExecContext(context.Background(), "RESET ROLE")
			c.ExecContext(context.Background(), "REVOKE EXECUTE ON FUNCTION "+identity+" FROM shared_smoke_reader")
		}()
		backendSQL := "SELECT pg_stat_reset_backend_stats($1::integer)"
		backendStmt, err := c.PrepareContext(p.Ctx, backendSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer backendStmt.Close()
		backendHeld := fixture.HeldStatement{Executor: c, Statement: backendStmt, SQL: backendSQL}
		var pid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		denied := fixture.ErrorDiagnostic(backendSQL, activityDiagnostic("42501", "permission denied for function pg_stat_reset_backend_stats", 0), pid)
		denied.SelectRows = true
		activityExec(t, p, c, "SET ROLE shared_smoke_reader")
		fixture.RunStep(t, p.Ctx, backendHeld, b.Recorder, denied)
		activityExec(t, p, c, "RESET ROLE")
		activityExec(t, p, c, "GRANT EXECUTE ON FUNCTION "+identity+" TO shared_smoke_reader")
		activityExec(t, p, c, "SET ROLE shared_smoke_reader")
		resetReplicationVoid(t, p, b, backendHeld, backendSQL, "pg_stat_reset_backend_stats", pid)
		activityExec(t, p, c, "RESET ROLE")
		activityExec(t, p, c, "REVOKE EXECUTE ON FUNCTION "+identity+" FROM shared_smoke_reader")
		activityExec(t, p, c, "SET ROLE shared_smoke_reader")
		fixture.RunStep(t, p.Ctx, backendHeld, b.Recorder, denied)
		activityExec(t, p, c, "RESET ROLE")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM shared_reset_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}
