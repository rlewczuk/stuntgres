package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTablespaceInfoPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tsi_reader"})
	p := fixture.NewScenario(t)
	tablespaceInfoBackends(t, p, func(t *testing.T, b fixture.Backend) {
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE tsi_reader NOLOGIN NOSUPERUSER", 0))
		run(fixture.Exec("CREATE TABLE tsi_private(a integer)", 0))
		run(fixture.Exec("REVOKE ALL ON tsi_private FROM PUBLIC", 0))
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		for _, name := range []string{"pg_tablespace_location", "pg_tablespace_databases"} {
			t.Run(name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				q := "SELECT " + name + "($1::oid) AS v"
				if name == "pg_tablespace_databases" {
					q = "SELECT v FROM pg_tablespace_databases($1::oid) AS t(v) WHERE v=(SELECT oid FROM pg_database WHERE datname=current_database())"
				}
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				defer c.ExecContext(p.Ctx, "RESET ROLE")
				defer func() {
					if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
						t.Error(err)
					}
					if _, err := c.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION pg_catalog."+name+"(oid) TO PUBLIC"); err != nil {
						t.Error(err)
					}
				}()
				success := func(arg any) {
					if name == "pg_tablespace_location" {
						var v any = ""
						if arg == nil {
							v = nil
						}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, tablespaceInfoSelect(q, "TEXT", [][]any{{v}}, arg))
					} else {
						if arg == nil {
							fixture.RunStep(t, p.Ctx, held, b.Recorder, tablespaceInfoSelect(q, "OID", nil, arg))
							return
						}
						oid := tablespaceInfoCurrent(t, p, c)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, tablespaceInfoSelect(q, "OID", [][]any{{tablespaceInfoOID(b, oid)}}, arg))
					}
				}
				run(fixture.Exec("SET ROLE tsi_reader", 0))
				success(int64(1663))
				success(nil)
				// Native authority is the real builtin's absent ACL, bootstrap owner,
				// and this freshly created NOSUPERUSER with no CREATE grant.
				run(fixture.Select("SELECT spcacl,spcowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS owns FROM pg_tablespace WHERE oid=1663", []string{"spcacl", "owns"}, []string{"_ACLITEM", "BOOL"}, [][]any{{nil, false}}))
				if b.PG {
					run(fixture.Select("SELECT has_tablespace_privilege(current_user,1663,'CREATE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{false}}))
				}
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog."+name+"(oid) FROM PUBLIC", 0))
				run(fixture.Exec("SET ROLE tsi_reader", 0))
				for _, arg := range []any{int64(1663), nil} {
					label := "nonnull"
					if arg == nil {
						label = "null"
					}
					for _, route := range []string{"held", "direct"} {
						t.Run("revoked/"+label+"/"+route, func(t *testing.T) {
							s := fixture.ErrorDiagnostic(q, tablespaceDiagnostic("42501", "permission denied for function "+name), arg)
							s.SelectRows = true
							s.ErrorStage = "execute"
							var x fixture.Executor = c
							if route == "held" {
								x = held
							}
							// The existing strict-scalar NULL proof removes this
							// non-invocation before EXECUTE admission on both backends.
							if name == "pg_tablespace_location" && arg == nil {
								s = tablespaceInfoSelect(q, "TEXT", [][]any{{nil}}, arg)
							}
							fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
						})
					}
				}
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog."+name+"(oid) TO PUBLIC", 0))
				run(fixture.Exec("SET ROLE tsi_reader", 0))
				success(int64(1663))
				success(nil)
				run(fixture.Exec("RESET ROLE", 0))
			})
		}
	})
}
