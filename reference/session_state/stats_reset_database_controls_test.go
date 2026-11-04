package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatsResetDatabaseControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"database_control_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		activityExec(t, p, c, "CREATE ROLE database_control_reader")
		defer c.ExecContext(context.Background(), "RESET ROLE")
		for _, f := range resetDatabaseFunctions {
			formal, arg := "", ""
			if f.strict {
				formal = "oid"
				arg = "0::oid"
			}
			identity := "pg_catalog." + f.name + "(" + formal + ")"
			activityExec(t, p, c, "GRANT EXECUTE ON FUNCTION "+identity+" TO database_control_reader")
			defer func() {
				c.ExecContext(context.Background(), "RESET ROLE")
				c.ExecContext(context.Background(), "REVOKE EXECUTE ON FUNCTION "+identity+" FROM database_control_reader")
			}()
			query := "SELECT " + f.name + "(" + arg + ")"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			activityExec(t, p, c, "SET ROLE database_control_reader")
			resetDatabaseValue(t, p, b, held, query, f.name, "")
			activityExec(t, p, c, "RESET ROLE")
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if !b.PG {
			query := "SELECT pg_stat_reset_single_table_counters(0::oid)"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			retained, err := c.QueryContext(p.Ctx, "SELECT pg_stat_reset(),pg_stat_reset_single_table_counters(NULL::oid)")
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
			for _, f := range resetDatabaseFunctions {
				var acl string
				if err := c.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1::oid", f.oid).Scan(&acl); err != nil || acl != "{postgres=X/postgres}" {
					t.Fatal("bootstrap ACL", acl, err)
				}
			}
			resetDatabaseValue(t, p, b, held, query, "pg_stat_reset_single_table_counters", "")
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			for _, f := range resetDatabaseFunctions {
				var acl string
				if err := c.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1::oid", f.oid).Scan(&acl); err != nil || acl != "{postgres=X/postgres,database_control_reader=X/postgres}" {
					t.Fatal("restored ACL", acl, err)
				}
			}
			activityExec(t, p, c, "SET ROLE database_control_reader")
			resetDatabaseValue(t, p, b, held, query, "pg_stat_reset_single_table_counters", "")
			activityExec(t, p, c, "RESET ROLE")
			activityExec(t, p, c, "REVOKE EXECUTE ON FUNCTION pg_stat_reset_single_table_counters(oid) FROM database_control_reader")
			activityExec(t, p, c, "SET ROLE database_control_reader")
			denied := fixture.ErrorDiagnostic(query, activityDiagnostic("42501", "permission denied for function pg_stat_reset_single_table_counters", 0))
			denied.SelectRows = true
			fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
			activityExec(t, p, c, "RESET ROLE")
			columns, err := retained.ColumnTypes()
			if err != nil || len(columns) != 2 || columns[0].DatabaseTypeName() != "VOID" || columns[1].DatabaseTypeName() != "VOID" {
				t.Fatal("retained metadata", columns, err)
			}
			if !retained.Next() {
				t.Fatal("retained row", retained.Err())
			}
			var value string
			var null any
			if err := retained.Scan(&value, &null); err != nil || value != "" || null != nil || retained.Next() || retained.Err() != nil {
				t.Fatal("retained values", value, null, err, retained.Err())
			}
			d := stuntgres.NewDriver()
			connector, err := d.OpenConnector("database-reset-independent")
			if err != nil {
				t.Fatal(err)
			}
			independent := sql.OpenDB(connector)
			defer independent.Close()
			for _, f := range resetDatabaseFunctions {
				var acl string
				if err := independent.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1::oid", f.oid).Scan(&acl); err != nil || acl != "{postgres=X/postgres}" {
					t.Fatal("independent Driver authority", acl, err)
				}
			}
		}
	})
}

func TestReferenceStatsResetDatabaseFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"database_smoke_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statsCreate(t, p, b, c, "reset_smoke_a", "reset_smoke_b")
		statsPublish(t, p, b, "reset_smoke_a", 2, fixture.Exec("INSERT INTO reset_smoke_a VALUES(1),(2)", 2))
		statsPublish(t, p, b, "reset_smoke_b", 3, fixture.Exec("INSERT INTO reset_smoke_b VALUES(1),(2),(3)", 3))
		resetDatabaseExec(t, p, c, "SET stats_fetch_consistency=none", "CREATE ROLE database_smoke_reader")
		defer func() {
			c.ExecContext(context.Background(), "RESET ROLE")
			c.ExecContext(context.Background(), "REVOKE EXECUTE ON FUNCTION pg_stat_reset_single_table_counters(oid) FROM database_smoke_reader")
		}()
		q := "SELECT pg_stat_reset_single_table_counters($1::oid)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		var oid int64
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT 'reset_smoke_a'::regclass::oid::bigint,current_database()").Scan(&oid, &database); err != nil {
			t.Fatal(err)
		}
		statsReportingWant(t, p, b, c, "reset_smoke_a", statsReportingVector{2, 0, 0, 2, 0, 2, 2})
		resetDatabaseValue(t, p, b, held, q, "pg_stat_reset_single_table_counters", "", oid)
		statsClear(t, p, b, c)
		statsReportingWant(t, p, b, c, "reset_smoke_a", statsReportingVector{})
		statsReportingWant(t, p, b, c, "reset_smoke_b", statsReportingVector{3, 0, 0, 3, 0, 3, 3})
		stamp := resetDatabaseTimestamp(t, p, c, database)
		resetDatabaseValue(t, p, b, held, q, "pg_stat_reset_single_table_counters", nil, nil)
		if current := resetDatabaseTimestamp(t, p, c, database); !resetDatabaseSameTime(current, stamp) {
			t.Fatal("strict NULL updated timestamp", current, stamp)
		}
		statsReportingRows(t, p, b, c, "reset_smoke_a", []int64{1, 2})
		statsReportingRows(t, p, b, c, "reset_smoke_b", []int64{1, 2, 3})
		denied := fixture.ErrorDiagnostic(q, activityDiagnostic("42501", "permission denied for function pg_stat_reset_single_table_counters", 0), oid)
		denied.SelectRows = true
		activityExec(t, p, c, "SET ROLE database_smoke_reader")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		resetDatabaseExec(t, p, c, "RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_stat_reset_single_table_counters(oid) TO database_smoke_reader", "SET ROLE database_smoke_reader")
		resetDatabaseValue(t, p, b, held, q, "pg_stat_reset_single_table_counters", "", oid)
		resetDatabaseExec(t, p, c, "RESET ROLE", "REVOKE EXECUTE ON FUNCTION pg_stat_reset_single_table_counters(oid) FROM database_smoke_reader", "SET ROLE database_smoke_reader")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		activityExec(t, p, c, "RESET ROLE")
		statsReportingWant(t, p, b, c, "reset_smoke_b", statsReportingVector{3, 0, 0, 3, 0, 3, 3})
		activityExec(t, p, c, "BEGIN")
		resetDatabaseValue(t, p, b, c, "SELECT pg_stat_reset()", "pg_stat_reset", "")
		if !resetDatabaseTimestamp(t, p, c, database).Valid {
			t.Fatal("database reset timestamp absent")
		}
		statsClear(t, p, b, c)
		statsReportingWant(t, p, b, c, "reset_smoke_a", statsReportingVector{})
		statsReportingWant(t, p, b, c, "reset_smoke_b", statsReportingVector{})
		statsReportingRows(t, p, b, c, "reset_smoke_a", []int64{1, 2})
		statsReportingRows(t, p, b, c, "reset_smoke_b", []int64{1, 2, 3})
		activityExec(t, p, c, "ROLLBACK")
		statsPublish(t, p, b, "reset_smoke_a", 1, fixture.Exec("INSERT INTO reset_smoke_a VALUES(3)", 1))
	})
}
