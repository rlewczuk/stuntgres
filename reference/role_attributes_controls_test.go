package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceRoleAttributesRetentionAndControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			roleReferenceCleanup(t, []string{"rac_actor", "rac_target"})
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				c, err := b.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
				run := func(s referenceStep) { roleAttributesRun(t, p, b, c, prepared, s) }
				run(referenceExec("CREATE ROLE rac_actor LOGIN CREATEROLE CREATEDB REPLICATION BYPASSRLS NOINHERIT CONNECTION LIMIT 2 PASSWORD 'role-attributes-fixture' VALID UNTIL '2030-01-02 03:04:05+00'", 0))
				run(referenceExec("CREATE TABLE rac_sentinel(id INT)", 0))
				run(referenceExec("INSERT INTO rac_sentinel VALUES(7)", 1))
				var password string
				if b.pg {
					if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT rolpassword FROM pg_authid WHERE rolname='rac_actor'").Scan(&password); err != nil {
						t.Fatal(err)
					}
				}
				run(referenceExec("COMMENT ON ROLE rac_actor IS 'retained comment'", 0))
				run(referenceExec("ALTER USER rac_actor NOCREATEDB", 0))
				checkFacts := func() {
					if b.pg {
						roleAttributesFacts(t, p, "rac_actor", []any{false, false, true, true, false, true, true, int64(2), false, "2030-01-02 03:04:05+00"})
						var after string
						if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT rolpassword FROM pg_authid WHERE rolname='rac_actor'").Scan(&after); err != nil || after != password {
							t.Error("omitted password changed", err)
						}
					}
				}
				checkFacts()
				retained, err := b.db.QueryContext(p.ctx, "SELECT current_user")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				if !b.pg {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(dump.String(), "rac_actor") || strings.Contains(dump.String(), "PASSWORD") || strings.Contains(dump.String(), "SCRAM") {
						t.Fatal("shared role metadata leaked into Dump", dump.String())
					}
					if err := stuntgres.Reset(p.ctx, p.native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE ROLE forbidden LOGIN PASSWORD 'leak';")); err == nil {
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
				if b.pg {
					bootstrap = "stuntgres"
				}
				if err := retained.Scan(&retainedName); err != nil || retainedName != bootstrap {
					t.Fatal("retained NAME value", retainedName, err)
				}
				if retained.Next() || retained.Err() != nil {
					t.Fatal("retained NAME extra/error", retained.Err())
				}
				retained.Close()
				run(referenceSelect("SELECT id FROM rac_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(referenceExec("SET SESSION AUTHORIZATION rac_actor", 0))
				run(referenceExec("CREATE ROLE rac_target", 0))
				run(referenceExec("ALTER ROLE rac_target LOGIN", 0))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				// DROP and COMMENT remain adjacent superuser gates, not CREATEROLE
				// authority expansion. PostgreSQL's broader COMMENT is oracle-only.
				if !b.pg {
					run(referenceExec("SET SESSION AUTHORIZATION rac_actor", 0))
					run(roleReferenceError("DROP ROLE rac_target", "42501", "permission denied to drop role", "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles.", prepared, "execute"))
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				}
			}
		})
	}
}
