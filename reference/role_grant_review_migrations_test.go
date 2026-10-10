package reference_test

import (
	"fmt"
	"github.com/lib/pq"
	"testing"
)

func TestReferenceRoleDependencyAddressDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			owner, reader := fmt.Sprintf("rd_%v_owner", prepared), fmt.Sprintf("rd_%v_reader", prepared)
			roleReferenceCleanup(t, []string{reader, owner})
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				c, err := backend.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, backend.recorder, s) }
				for _, name := range []string{owner, reader} {
					run(referenceExec("CREATE USER "+name, 0))
				}
				if backend.pg {
					run(referenceExec("GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC", 0))
				}
				run(referenceExec("CREATE TABLE secret(a INT,b INT)", 0))
				run(referenceExec("INSERT INTO secret VALUES(7,8)", 1))
				run(referenceExec(`CREATE TABLE "column table"("odd column" INT)`, 0))
				run(referenceExec(`INSERT INTO "column table" VALUES(7)`, 1))
				run(referenceExec("GRANT SELECT(a) ON secret TO "+reader, 0))
				run(roleReferenceError("DROP ROLE "+reader, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", reader), "privileges for column a of table secret", prepared, "execute"))
				// Reverse grant order still reports stable SubID order; duplicate
				// grants do not produce duplicate dependency descriptions.
				for _, query := range []string{"GRANT SELECT(b,a) ON secret TO " + reader, `GRANT SELECT("odd column") ON "column table" TO ` + reader} {
					run(referenceExec(query, 0))
				}
				run(referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
				run(referenceSelect("SELECT a,b FROM secret", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(8)}}))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				// REL_18_0 getObjectDescription uses raw attname, while the
				// relation description applies quote_qualified_identifier.
				detail := "privileges for column a of table secret\nprivileges for column b of table secret\nprivileges for column odd column of table \"column table\""
				run(roleReferenceError("DROP ROLE "+reader, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", reader), detail, prepared, "execute"))
				run(referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
				run(referenceSelect("SELECT a,b FROM secret", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(8)}}))
				run(referenceSelect(`SELECT "odd column" FROM "column table"`, []string{"odd column"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				for _, query := range []string{"REVOKE SELECT(a,b) ON secret FROM " + reader, `REVOKE SELECT("odd column") ON "column table" FROM ` + reader} {
					run(referenceExec(query, 0))
				}
				run(referenceExec("DROP ROLE "+reader, 0))
				run(referenceExec("SET SESSION AUTHORIZATION "+owner, 0))
				var previous int64
				for _, name := range []string{"z", "a", "odd table", "select", "quote\"table", "integer", "Mixed"} {
					run(referenceExec("CREATE TABLE "+pq.QuoteIdentifier(name)+"(id INT)", 0))
					var oid int64
					if backend.pg {
						if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_class WHERE relnamespace='public'::regnamespace AND relname=$1", name).Scan(&oid); err != nil || oid <= previous {
							t.Fatal("creation identity ordering", name, oid, previous, err)
						}
						previous = oid
					}
				}
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				run(referenceExec("GRANT SELECT(id) ON z TO "+owner, 0))
				detail = "owner of table z\nowner of table a\nowner of table \"odd table\"\nowner of table \"select\"\nowner of table \"quote\"\"table\"\nowner of table \"integer\"\nowner of table \"Mixed\""
				run(roleReferenceError("DROP ROLE "+owner, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", owner), detail, prepared, "execute"))
				var count int
				if backend.pg {
					if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_roles WHERE rolname=$1", owner).Scan(&count); err != nil || count != 1 {
						t.Fatal("owner disappeared", count, err)
					}
				} else {
					run(referenceExec("SET SESSION AUTHORIZATION "+owner, 0))
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				}
			}
		})
	}
}
func TestReferenceRolePublicGrantOptionsInvalid(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE secret(id INT)", 0))
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO secret VALUES(7)", 1))
				var before, after string
				acl := "SELECT COALESCE(relacl::text,'absent') FROM pg_class WHERE oid='public.secret'::regclass"
				if backend.pg {
					if err := referenceAdmin.QueryRowContext(p.ctx, acl).Scan(&before); err != nil {
						t.Fatal(err)
					}
				}
				query := "GRANT SELECT ON secret TO PUBLIC WITH GRANT OPTION"
				var err error
				if prepared {
					stmt, prepareErr := backend.db.PrepareContext(p.ctx, query)
					if prepareErr != nil {
						t.Fatal("unexpected Prepare rejection", prepareErr)
					}
					defer stmt.Close()
					_, err = stmt.ExecContext(p.ctx)
				} else {
					_, err = backend.db.ExecContext(p.ctx, query)
				}
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0LP01", Message: "grant options can only be granted to roles"}
				referenceAssertDiagnostic(t, err, &want)
				if backend.pg {
					if err := referenceAdmin.QueryRowContext(p.ctx, acl).Scan(&after); err != nil || after != before {
						t.Fatal("invalid grant changed ACL", before, after, err)
					}
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT id FROM secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		})
	}
}
