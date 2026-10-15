package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func dtpReadRows(t *testing.T, rows *sql.Rows) [][]string {
	t.Helper()
	defer rows.Close()
	var got [][]string
	for rows.Next() {
		row := make([]string, 5)
		if err := rows.Scan(&row[0], &row[1], &row[2], &row[3], &row[4]); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestReferenceDataTypePrivilegesLifecycle(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE dtp_lifecycle(a integer,b varchar(7)[])", 0))
		q := "SELECT * FROM information_schema.data_type_privileges WHERE object_schema='public' AND object_name='dtp_lifecycle' ORDER BY dtd_identifier"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		check := func(ids ...string) {
			rows, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			var want [][]string
			for _, id := range ids {
				want = append(want, []string{database, "public", "dtp_lifecycle", "TABLE", id})
			}
			if got := dtpReadRows(t, rows); !reflect.DeepEqual(got, want) {
				t.Fatalf("held execution got %v want %v", got, want)
			}
		}
		check("1", "2")
		dtpActor(run, "dtp_reader", func() { check() })
		run(fixture.Exec("GRANT SELECT(b) ON dtp_lifecycle TO dtp_reader", 0))
		dtpActor(run, "dtp_reader", func() { check("2") })
		run(fixture.Exec("REVOKE SELECT(b) ON dtp_lifecycle FROM dtp_reader", 0))
		dtpActor(run, "dtp_reader", func() { check() })
		run(fixture.Exec("ALTER TABLE dtp_lifecycle DROP COLUMN a", 0))
		run(fixture.Exec("ALTER TABLE dtp_lifecycle ADD COLUMN c integer[][]", 0))
		check("2", "3")
		run(fixture.Exec("ALTER TABLE dtp_lifecycle RENAME TO dtp_lifecycle_renamed", 0))
		check()
		run(fixture.Exec("DROP TABLE dtp_lifecycle_renamed", 0))
		check()
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		// A pre-canceled pool acquisition is rejected at database/sql's adapter
		// boundary, before borrowing or poisoning the scenario's physical Conn.
		if rows, err := b.DB.QueryContext(canceled, q); !errors.Is(err, context.Canceled) || rows != nil {
			if rows != nil {
				rows.Close()
			}
			t.Fatal("canceled query", err)
		}
		check()
	})
}

func TestReferenceDataTypePrivilegesControls(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{"CREATE DOMAIN dtp_control_domain AS varchar(7)", "CREATE TYPE dtp_control_type AS (a integer,b varchar(7)[])", "CREATE TABLE dtp_control_table(a dtp_control_domain,b dtp_control_type,c integer[][])"} {
			run(fixture.Exec(q, 0))
		}
		where := "object_schema='public' AND object_name IN ('dtp_control_domain','dtp_control_type','dtp_control_table')"
		want := dtpRows("public", "dtp_control_domain", "DOMAIN", "1")
		want = append(want, dtpRows("public", "dtp_control_type", "USER-DEFINED TYPE", "1", "2")...)
		want = append(want, dtpRows("public", "dtp_control_table", "TABLE", "1", "2", "3")...)
		run(dtpProjection(t, p, c, where, want))
		if b.PG {
			// PostgreSQL has real DDL, not a fabricated native Reset API.
			run(fixture.Exec("DROP TABLE dtp_control_table", 0))
			run(fixture.Exec("DROP TYPE dtp_control_type", 0))
			run(fixture.Exec("DROP DOMAIN dtp_control_domain", 0))
			run(dtpProjection(t, p, c, where, nil))
			return
		}
		q := "SELECT * FROM information_schema.data_type_privileges WHERE " + where + " ORDER BY object_name,dtd_identifier"
		old, err := b.DB.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(dtpProjection(t, p, c, where, nil))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(dtpProjection(t, p, c, where, want))
		got := dtpReadRows(t, old)
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		ordered := append(dtpRows("public", "dtp_control_domain", "DOMAIN", "1"), dtpRows("public", "dtp_control_table", "TABLE", "1", "2", "3")...)
		ordered = append(ordered, dtpRows("public", "dtp_control_type", "USER-DEFINED TYPE", "1", "2")...)
		var expected [][]string
		for _, row := range ordered {
			expected = append(expected, []string{database, row[0].(string), row[1].(string), row[2].(string), row[3].(string)})
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("retained rows got %v want %v", got, expected)
		}
		// A failed staged replacement cannot disturb this actual inventory.
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString("CREATE DOMAIN dtp_failed AS integer; CREATE TABLE dtp_bad(a dtp_missing);")); err == nil {
			t.Fatal("invalid Restore succeeded")
		}
		run(dtpProjection(t, p, c, where, want))
		run(dtpProjection(t, p, c, "object_schema='public' AND object_name='dtp_failed'", nil))
	})
}

func TestReferenceDataTypePrivilegesFeatureSmoke(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{"CREATE DOMAIN dtp_smoke_domain AS varchar(7)", "CREATE TYPE dtp_smoke_composite AS (x integer,y varchar(7),z integer[])", "CREATE TYPE dtp_smoke_unused AS ENUM ('a','b')", "CREATE TABLE dtp_smoke_table(a integer,b varchar(7),c dtp_smoke_domain,d integer[][])"} {
			run(fixture.Exec(q, 0))
		}
		where := "object_schema='public' AND object_name IN ('dtp_smoke_domain','dtp_smoke_composite','dtp_smoke_unused','dtp_smoke_table')"
		types := dtpRows("public", "dtp_smoke_domain", "DOMAIN", "1")
		types = append(types, dtpRows("public", "dtp_smoke_composite", "USER-DEFINED TYPE", "1", "2", "3")...)
		owner := append(append([][]any{}, types...), dtpRows("public", "dtp_smoke_table", "TABLE", "1", "2", "3", "4")...)
		run(dtpProjection(t, p, c, where, owner))
		dtpActor(run, "dtp_reader", func() { run(dtpProjection(t, p, c, where, types)) })
		run(fixture.Exec("GRANT SELECT(b) ON dtp_smoke_table TO dtp_reader", 0))
		dtpActor(run, "dtp_reader", func() {
			run(dtpProjection(t, p, c, where, append(append([][]any{}, types...), dtpRows("public", "dtp_smoke_table", "TABLE", "2")...)))
		})
		run(fixture.Exec("REVOKE SELECT(b) ON dtp_smoke_table FROM dtp_reader", 0))
		dtpActor(run, "dtp_reader", func() {
			run(dtpProjection(t, p, c, where, types))
			run(dtpProjection(t, p, c, "object_schema='information_schema' AND object_name='sql_identifier' AND object_type='DOMAIN'", dtpRows("information_schema", "sql_identifier", "DOMAIN", "1")))
		})
		run(fixture.Exec("ALTER TABLE dtp_smoke_table DROP COLUMN b", 0))
		run(fixture.Exec("ALTER TABLE dtp_smoke_table ADD COLUMN e varchar(7)[]", 0))
		run(dtpProjection(t, p, c, where, append(append([][]any{}, types...), dtpRows("public", "dtp_smoke_table", "TABLE", "1", "3", "4", "5")...)))
		run(fixture.ImplicitError("SELECT missing FROM information_schema.data_type_privileges", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}))
	})
}
