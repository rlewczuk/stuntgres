package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRelationOptionsAuthority(t *testing.T) {
	relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		roles := []string{"ro_owner", "ro_member", "ro_grantee", "ro_select", "ro_maintain", "ro_schema_owner"}
		fixture.RoleReferenceCleanup(t, roles)
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		for _, role := range roles {
			run(fixture.Exec("CREATE ROLE "+role, 0))
		}
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO ro_owner,ro_member,ro_grantee,ro_select,ro_maintain,ro_schema_owner", 0))
		relationOptionsSeed(run)
		run(fixture.Exec("ALTER TABLE ro OWNER TO ro_owner", 0))
		run(fixture.Exec("GRANT ALL ON TABLE ro TO ro_grantee", 0))
		run(fixture.Exec("GRANT MAINTAIN ON TABLE ro TO ro_grantee", 0))
		run(fixture.Exec("GRANT ro_owner TO ro_member", 0))
		run(fixture.Exec("GRANT SELECT ON TABLE ro TO ro_select", 0))
		run(fixture.Exec("GRANT MAINTAIN ON TABLE ro TO ro_maintain", 0))
		for _, role := range []string{"ro_owner", "ro_member"} {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(fixture.Exec("ALTER TABLE ro SET(fillfactor=80)", 0))
			run(relationOptionsState("ro", "fillfactor=80"))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("ALTER TABLE ro SET(fillfactor=70)", 0)) // superuser
		run(fixture.Exec("CREATE SCHEMA ro_owned AUTHORIZATION ro_schema_owner", 0))
		defer b.DB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS ro_owned CASCADE")
		run(fixture.Exec("CREATE TABLE ro_owned.ro(id INT)", 0))
		run(fixture.Exec("ALTER TABLE ro_owned.ro OWNER TO ro_owner", 0))
		for _, tc := range []struct{ role, target string }{{"ro_grantee", "ro"}, {"ro_select", "ro"}, {"ro_maintain", "ro"}, {"ro_schema_owner", "ro_owned.ro"}} {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+tc.role, 0))
			run(relationOptionsError("ALTER TABLE "+tc.target+" SET(fillfactor=90)", "42501", "must be owner of table ro", ""))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(relationOptionsState("ro", "fillfactor=70"))
		run(relationOptionsState("ro_owned.ro"))
		run(fixture.Exec("SET SESSION AUTHORIZATION ro_member", 0))
		stmt, err := c.PrepareContext(p.Ctx, "ALTER TABLE ro SET(fillfactor=90)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("REVOKE ro_owner FROM ro_member", 0))
		_, err = stmt.ExecContext(p.Ctx)
		want := relationOptionsDiagnostic("42501", "must be owner of table ro", "")
		fixture.AssertDiagnostic(t, err, &want)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(relationOptionsState("ro", "fillfactor=70"))
		// Preparation remains effect-free even in read-only mode; execution is gated
		// before missing-target lookup or option validation.
		run(fixture.Exec("BEGIN READ ONLY", 0))
		for _, query := range []string{"ALTER TABLE ro SET(fillfactor=80)", "ALTER TABLE ro_missing SET(bogus=1)"} {
			s, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal("read-only Prepare", err)
			}
			_, err = s.ExecContext(p.Ctx)
			s.Close()
			want := relationOptionsDiagnostic("25006", "cannot execute ALTER TABLE in a read-only transaction", "")
			fixture.AssertDiagnostic(t, err, &want)
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("BEGIN READ ONLY", 0))
		}
		run(fixture.Exec("ROLLBACK", 0))
		run(relationOptionsState("ro", "fillfactor=70"))
		var notices []fixture.Diagnostic
		_, noticeConn, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		defer noticeConn.Close()
		s := fixture.Exec("ALTER TABLE IF EXISTS ro_missing SET(fillfactor=70)", 0)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, noticeConn, rec, s)
		wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "ro_missing" does not exist, skipping`}
		got := fixture.NoticeWitnesses(t, b.PG, notices, wantNotice)
		if !reflect.DeepEqual(got, []fixture.Diagnostic{wantNotice}) {
			t.Fatal(got)
		}
		run(fixture.Exec("BEGIN", 0))
		run(relationOptionsError("ALTER TABLE ro SET(bogus=1)", "22023", `unrecognized parameter "bogus"`, ""))
		run(fixture.ErrorDiagnostic("ALTER TABLE IF EXISTS ro_missing SET(fillfactor=70)", fixture.TransactionAborted()))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("DROP TABLE ro,ro_owned.ro", 0))
		run(fixture.Exec("DROP SCHEMA ro_owned", 0))
		run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA public FROM ro_owner,ro_member,ro_grantee,ro_select,ro_maintain,ro_schema_owner", 0))
		for _, role := range roles {
			run(fixture.Exec("DROP ROLE "+role, 0))
		}
	})
}

func TestReferenceRelationOptionsPrepared(t *testing.T) {
	relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		relationOptionsSeed(run)
		var oid, rowType int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid,reltype FROM pg_class WHERE oid='ro'::regclass").Scan(&oid, &rowType); err != nil {
			t.Fatal(err)
		}
		baseline := ""
		if !b.PG {
			baseline = relationOptionsDump(t, p)
		}
		alter, err := c.PrepareContext(p.Ctx, "ALTER TABLE ro SET(fillfactor=80)")
		if err != nil {
			t.Fatal(err)
		}
		defer alter.Close()
		if err := c.Raw(func(raw any) error {
			stmt, err := raw.(driver.ConnPrepareContext).PrepareContext(p.Ctx, "ALTER TABLE ro RESET(fillfactor)")
			if err != nil {
				return err
			}
			defer stmt.Close()
			if stmt.NumInput() != 0 {
				t.Fatalf("ALTER NumInput=%d", stmt.NumInput())
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		run(relationOptionsState("ro", "fillfactor=70"))
		if !b.PG && relationOptionsDump(t, p) != baseline {
			t.Fatal("Prepare changed dump")
		}
		selectStmt, err := c.PrepareContext(p.Ctx, "SELECT id,payload FROM ro ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer selectStmt.Close()
		catalogStmt, err := c.PrepareContext(p.Ctx, "SELECT reloptions FROM pg_class WHERE oid='ro'::regclass")
		if err != nil {
			t.Fatal(err)
		}
		defer catalogStmt.Close()
		// Retain an open result on a separate session; option SHARE UPDATE EXCLUSIVE
		// is compatible with its ordinary read lock.
		heldConn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer heldConn.Close()
		held, err := heldConn.QueryContext(p.Ctx, "SELECT id,payload FROM ro ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		result, err := alter.ExecContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 0)
		commandRows, err := alter.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		columns, columnErr := commandRows.Columns()
		if columnErr != nil || len(columns) != 0 || commandRows.Next() || commandRows.Err() != nil {
			commandRows.Close()
			t.Fatal("ALTER result metadata", columns, columnErr, commandRows.Err())
		}
		commandRows.Close()
		consumeCatalog := func(want any) {
			rows, err := catalogStmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "_TEXT" {
				t.Fatal(types, err)
			}
			var value any
			if !rows.Next() {
				t.Fatal("missing catalog row", rows.Err())
			}
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if data, ok := value.([]byte); ok {
				value = string(data)
			}
			if !reflect.DeepEqual(value, want) || rows.Next() || rows.Err() != nil {
				t.Fatal(value, want, rows.Err())
			}
		}
		consumeCatalog("{fillfactor=80}")
		consumeRows := func(rows *sql.Rows) {
			defer rows.Close()
			cols, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(cols, []string{"id", "payload"}) {
				t.Fatal(cols, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
				t.Fatal(types, err)
			}
			var id int64
			var payload string
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			if err := rows.Scan(&id, &payload); err != nil || id != 1 || payload != "sentinel" {
				t.Fatal(id, payload, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("unexpected rows", rows.Err())
			}
		}
		consumeRows(held)
		rows, err := selectStmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		consumeRows(rows)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER TABLE ro RESET(fillfactor)", 0))
		run(fixture.Exec("COMMIT", 0))
		consumeCatalog(nil)
		run(fixture.Select("SELECT oid=$1::oid AS same_oid,reltype=$2::oid AS same_type FROM pg_class WHERE oid='ro'::regclass", []string{"same_oid", "same_type"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, oid, rowType))
		bad, err := c.PrepareContext(p.Ctx, "ALTER TABLE ro SET(fillfactor=9)")
		if err != nil {
			t.Fatal("semantic validation during Prepare", err)
		}
		defer bad.Close()
		_, err = bad.ExecContext(p.Ctx)
		want := relationOptionsDiagnostic("22023", `value 9 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`)
		fixture.AssertDiagnostic(t, err, &want)
		run(relationOptionsState("ro"))
		run(relationOptionsRows("ro"))
	})
}
