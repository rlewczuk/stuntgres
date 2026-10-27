package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleAttributesRetentionAndControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rac_actor", "rac_target"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				run := func(s fixture.Step) { fixture.RoleAttributesRun(t, p, b, c, prepared, s) }
				run(fixture.Exec("CREATE ROLE rac_actor LOGIN CREATEROLE CREATEDB REPLICATION BYPASSRLS NOINHERIT CONNECTION LIMIT 2 PASSWORD 'role-attributes-fixture' VALID UNTIL '2030-01-02 03:04:05+00'", 0))
				run(fixture.Exec("CREATE TABLE rac_sentinel(id INT)", 0))
				run(fixture.Exec("INSERT INTO rac_sentinel VALUES(7)", 1))
				var password string
				if b.PG {
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT rolpassword FROM pg_authid WHERE rolname='rac_actor'").Scan(&password); err != nil {
						t.Fatal(err)
					}
				}
				run(fixture.Exec("COMMENT ON ROLE rac_actor IS 'retained comment'", 0))
				run(fixture.Exec("ALTER USER rac_actor NOCREATEDB", 0))
				checkFacts := func() {
					if b.PG {
						roleAttributesFacts(t, p, "rac_actor", []any{false, false, true, true, false, true, true, int64(2), false, "2030-01-02 03:04:05+00"})
						var after string
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT rolpassword FROM pg_authid WHERE rolname='rac_actor'").Scan(&after); err != nil || after != password {
							t.Error("omitted password changed", err)
						}
					}
				}
				checkFacts()
				retained, err := b.DB.QueryContext(p.Ctx, "SELECT current_user")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(dump.String(), "rac_actor") || strings.Contains(dump.String(), "PASSWORD") || strings.Contains(dump.String(), "SCRAM") {
						t.Fatal("shared role metadata leaked into Dump", dump.String())
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE ROLE forbidden LOGIN PASSWORD 'leak';")); err == nil {
						t.Fatal("Restore accepted role mutation")
					}
				}
				checkFacts()
				columns, err := retained.Columns()
				if err != nil || len(columns) != 1 || columns[0] != "current_user" {
					t.Fatal("retained NAME columns", columns, err)
				}
				types, err := retained.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "NAME" {
					t.Fatal("retained NAME type", types, err)
				}
				if !retained.Next() {
					t.Fatal("retained NAME missing", retained.Err())
				}
				var retainedName string
				bootstrap := "postgres"
				if b.PG {
					bootstrap = "stuntgres"
				}
				if err := retained.Scan(&retainedName); err != nil || retainedName != bootstrap {
					t.Fatal("retained NAME value", retainedName, err)
				}
				if retained.Next() || retained.Err() != nil {
					t.Fatal("retained NAME extra/error", retained.Err())
				}
				retained.Close()
				run(fixture.Select("SELECT id FROM rac_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(fixture.Exec("SET SESSION AUTHORIZATION rac_actor", 0))
				run(fixture.Exec("CREATE ROLE rac_target", 0))
				run(fixture.Exec("ALTER ROLE rac_target LOGIN", 0))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				// DROP and COMMENT remain adjacent superuser gates, not CREATEROLE
				// authority expansion. PostgreSQL's broader COMMENT is oracle-only.
				if !b.PG {
					run(fixture.Exec("SET SESSION AUTHORIZATION rac_actor", 0))
					run(fixture.RoleReferenceError("DROP ROLE rac_target", "42501", "permission denied to drop role", "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles.", prepared, "execute"))
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				}
			}
		})
	}
}
