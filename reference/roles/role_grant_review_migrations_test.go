package reference_test

import (
	"fmt"
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleDependencyAddressDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			owner, reader := fmt.Sprintf("rd_%v_owner", prepared), fmt.Sprintf("rd_%v_reader", prepared)
			fixture.RoleReferenceCleanup(t, []string{reader, owner})
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				c, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
				for _, name := range []string{owner, reader} {
					run(fixture.Exec("CREATE USER "+name, 0))
				}
				if backend.PG {
					run(fixture.Exec("GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC", 0))
				}
				run(fixture.Exec("CREATE TABLE secret(a INT,b INT)", 0))
				run(fixture.Exec("INSERT INTO secret VALUES(7,8)", 1))
				run(fixture.Exec(`CREATE TABLE "column table"("odd column" INT)`, 0))
				run(fixture.Exec(`INSERT INTO "column table" VALUES(7)`, 1))
				run(fixture.Exec("GRANT SELECT(a) ON secret TO "+reader, 0))
				run(fixture.RoleReferenceError("DROP ROLE "+reader, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", reader), "privileges for column a of table secret", prepared, "execute"))
				// Reverse grant order still reports stable SubID order; duplicate
				// grants do not produce duplicate dependency descriptions.
				for _, query := range []string{"GRANT SELECT(b,a) ON secret TO " + reader, `GRANT SELECT("odd column") ON "column table" TO ` + reader} {
					run(fixture.Exec(query, 0))
				}
				run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
				run(fixture.Select("SELECT a,b FROM secret", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(8)}}))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				// REL_18_0 getObjectDescription uses raw attname, while the
				// relation description applies quote_qualified_identifier.
				detail := "privileges for column a of table secret\nprivileges for column b of table secret\nprivileges for column odd column of table \"column table\""
				run(fixture.RoleReferenceError("DROP ROLE "+reader, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", reader), detail, prepared, "execute"))
				run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
				run(fixture.Select("SELECT a,b FROM secret", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(8)}}))
				run(fixture.Select(`SELECT "odd column" FROM "column table"`, []string{"odd column"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				for _, query := range []string{"REVOKE SELECT(a,b) ON secret FROM " + reader, `REVOKE SELECT("odd column") ON "column table" FROM ` + reader} {
					run(fixture.Exec(query, 0))
				}
				run(fixture.Exec("DROP ROLE "+reader, 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
				var previous int64
				for _, name := range []string{"z", "a", "odd table", "select", "quote\"table", "integer", "Mixed"} {
					run(fixture.Exec("CREATE TABLE "+pq.QuoteIdentifier(name)+"(id INT)", 0))
					var oid int64
					if backend.PG {
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relnamespace='public'::regnamespace AND relname=$1", name).Scan(&oid); err != nil || oid <= previous {
							t.Fatal("creation identity ordering", name, oid, previous, err)
						}
						previous = oid
					}
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("GRANT SELECT(id) ON z TO "+owner, 0))
				detail = "owner of table z\nowner of table a\nowner of table \"odd table\"\nowner of table \"select\"\nowner of table \"quote\"\"table\"\nowner of table \"integer\"\nowner of table \"Mixed\""
				run(fixture.RoleReferenceError("DROP ROLE "+owner, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", owner), detail, prepared, "execute"))
				var count int
				if backend.PG {
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname=$1", owner).Scan(&count); err != nil || count != 1 {
						t.Fatal("owner disappeared", count, err)
					}
				} else {
					run(fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				}
			}
		})
	}
}
func TestReferenceRolePublicGrantOptionsInvalid(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE secret(id INT)", 0))
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO secret VALUES(7)", 1))
				var before, after string
				acl := "SELECT COALESCE(relacl::text,'absent') FROM pg_class WHERE oid='public.secret'::regclass"
				if backend.PG {
					if err := fixture.Admin.QueryRowContext(p.Ctx, acl).Scan(&before); err != nil {
						t.Fatal(err)
					}
				}
				query := "GRANT SELECT ON secret TO PUBLIC WITH GRANT OPTION"
				var err error
				if prepared {
					stmt, prepareErr := backend.DB.PrepareContext(p.Ctx, query)
					if prepareErr != nil {
						t.Fatal("unexpected Prepare rejection", prepareErr)
					}
					defer stmt.Close()
					_, err = stmt.ExecContext(p.Ctx)
				} else {
					_, err = backend.DB.ExecContext(p.Ctx, query)
				}
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0LP01", Message: "grant options can only be granted to roles"}
				fixture.AssertDiagnostic(t, err, &want)
				if backend.PG {
					if err := fixture.Admin.QueryRowContext(p.Ctx, acl).Scan(&after); err != nil || after != before {
						t.Fatal("invalid grant changed ACL", before, after, err)
					}
				}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT id FROM secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		})
	}
}
