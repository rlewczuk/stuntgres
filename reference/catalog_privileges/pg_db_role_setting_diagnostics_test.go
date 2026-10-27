package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func pgDBRoleSettingCanceled(t *testing.T, err error) {
	t.Helper()
	var state interface{ SQLState() string }
	if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
		t.Fatalf("ordinary cancellation: %T %v", err, err)
	}
}

func TestReferencePGDBRoleSettingDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"dbrole18_denied"})
	pgDBRoleSettingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ q, code, message, position string }{
			{"SELECT missing FROM pg_catalog.pg_db_role_setting", "42703", "column \"missing\" does not exist", "8"},
			{"SELECT oid FROM pg_catalog.pg_db_role_setting", "42703", "column \"oid\" does not exist", "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_db_role_setting", "42703", "column \"missing\" does not exist", "14"},
			{"SELECT setrole FROM pg_catalog.pg_db_role_setting_missing", "42P01", "relation \"pg_catalog.pg_db_role_setting_missing\" does not exist", "21"},
			{"SELECT 'bad'::oid FROM pg_db_role_setting WHERE FALSE", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
			{"SELECT '4294967296'::oid FROM pg_db_role_setting", "22003", "value \"4294967296\" is out of range for type oid", "8"},
		} {
			for _, plain := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.SelectRows, s.NonContext, s.Prepared, s.ErrorStage = true, plain, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		}
		q := "SELECT * FROM pg_db_role_setting WHERE setdatabase=$1"
		held := fixture.ViewHold(t, p, b, q)
		for _, tc := range []struct{ value, code, message string }{
			{"bad", "22P02", "invalid input syntax for type oid: \"bad\""},
			{"", "22P02", "invalid input syntax for type oid: \"\""},
			{"4294967296", "22003", "value \"4294967296\" is out of range for type oid"},
		} {
			portal := "unnamed portal parameter $1 = '...'"
			if tc.value == "" {
				portal = "unnamed portal parameter $1 = ''"
			}
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: portal}, tc.value)
			s.SelectRows, s.ErrorStage = true, "execute"
			for _, plain := range []bool{false, true} {
				s.NonContext = plain
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			}
			s.NonContext = false
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgDBRoleSettingEmpty(q, b.PG, int64(0)))
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := b.DB.QueryContext(canceled, q, int64(0))
		if rows != nil {
			rows.Close()
			t.Fatal("canceled query published Rows")
		}
		pgDBRoleSettingCanceled(t, err)
		stmt, err := b.DB.PrepareContext(canceled, q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("canceled prepare published statement")
		}
		pgDBRoleSettingCanceled(t, err)
		rows, err = held.Statement.QueryContext(canceled, int64(0))
		if rows != nil {
			rows.Close()
			t.Fatal("canceled held query published Rows")
		}
		pgDBRoleSettingCanceled(t, err)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgDBRoleSettingEmpty(q, b.PG, int64(0)))
		{
			// Source ACLs are shared public SQL on both backends.
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer func() {
				if _, err := conn.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
					t.Error(err)
				}
				if _, err := conn.ExecContext(p.Ctx, "REVOKE SELECT(setdatabase) ON pg_catalog.pg_db_role_setting FROM PUBLIC"); err != nil {
					t.Error(err)
				}
				if _, err := conn.ExecContext(p.Ctx, "GRANT SELECT ON pg_catalog.pg_db_role_setting TO PUBLIC"); err != nil {
					t.Error(err)
				}
			}()
			const columnQuery = "SELECT setdatabase FROM pg_catalog.pg_db_role_setting"
			columnStmt, err := conn.PrepareContext(p.Ctx, columnQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer columnStmt.Close()
			columnHeld := fixture.HeldStatement{Executor: conn, Statement: columnStmt, SQL: columnQuery}
			columnRows := fixture.Select(columnQuery, []string{"setdatabase"}, []string{"OID"}, nil)
			for _, sql := range []string{"CREATE ROLE dbrole18_denied", "REVOKE SELECT ON pg_catalog.pg_db_role_setting FROM PUBLIC", "SET ROLE dbrole18_denied"} {
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(sql, 0))
			}
			s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_db_role_setting", fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_db_role_setting"})
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			acl := func(command string) {
				for _, sql := range []string{"RESET ROLE", command, "SET ROLE dbrole18_denied"} {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(sql, 0))
				}
			}
			acl("GRANT SELECT(setdatabase) ON pg_catalog.pg_db_role_setting TO PUBLIC")
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, columnRows)
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Prepared(columnRows))
			fixture.RunStep(t, p.Ctx, columnHeld, b.Recorder, columnRows)
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			other := fixture.ImplicitError("SELECT setrole FROM pg_catalog.pg_db_role_setting", fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_db_role_setting"})
			other.SelectRows, other.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, other)
			acl("REVOKE SELECT(setdatabase) ON pg_catalog.pg_db_role_setting FROM PUBLIC")
			deniedColumn := fixture.ImplicitError(columnQuery, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_db_role_setting"})
			deniedColumn.SelectRows, deniedColumn.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, columnHeld, b.Recorder, deniedColumn)
			acl("GRANT SELECT(setdatabase) ON pg_catalog.pg_db_role_setting TO PUBLIC")
			fixture.RunStep(t, p.Ctx, columnHeld, b.Recorder, columnRows)
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			acl("REVOKE SELECT(setdatabase) ON pg_catalog.pg_db_role_setting FROM PUBLIC")
			acl("GRANT SELECT ON pg_catalog.pg_db_role_setting TO PUBLIC")
			fixture.RunStep(t, p.Ctx, columnHeld, b.Recorder, columnRows)
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, pgDBRoleSettingEmpty("SELECT * FROM pg_catalog.pg_db_role_setting", b.PG))
			if b.PG {
				return
			}
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE dbrole18_guard(id integer)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO dbrole18_guard VALUES(18)", 1))
		probe := func() {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgDBRoleSettingEmpty(q, false, int64(0)))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id FROM dbrole18_guard", []string{"id"}, []string{"INT4"}, [][]any{{int64(18)}}))
		}
		for _, tc := range []struct{ q, stage string }{
			{"INSERT INTO pg_db_role_setting(setdatabase,setrole,setconfig) VALUES(0,0,NULL)", "execute"},
			{"UPDATE pg_db_role_setting SET setrole=0 WHERE FALSE", "prepare"},
			{"DELETE FROM pg_db_role_setting WHERE FALSE", "execute"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = tc.stage
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				probe()
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT * FROM pg_db_role_setting WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			probe()
		}
		probe()
		live, stop := context.WithCancel(p.Ctx)
		rows, err = held.Statement.QueryContext(live, int64(0))
		if err != nil {
			stop()
			t.Fatal(err)
		}
		defer rows.Close()
		stop()
		if rows.Next() {
			t.Fatal("canceled empty source returned a row")
		}
		if err := rows.Err(); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		probe()
	})
}
