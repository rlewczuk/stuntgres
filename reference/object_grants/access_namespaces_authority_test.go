package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func accessNamespacesStoredAuthority(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), objects []accessExecutionObject) {
	t.Helper()
	run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
	for i, o := range objects {
		body := fmt.Sprintf("SELECT %s(%d::oid,'%s') AS v", o.function, o.oid, o.privilege)
		view := fmt.Sprintf("an_invoker_%d", i)
		function := fmt.Sprintf("an_inquiry_%d", i)
		run(fixture.Exec("CREATE VIEW "+view+" WITH(security_invoker=true) AS "+body, 0))
		run(fixture.Exec("GRANT SELECT ON "+view+" TO og_object_reader", 0))
		run(fixture.Exec("CREATE FUNCTION "+function+"() RETURNS boolean LANGUAGE SQL BEGIN ATOMIC "+body+"; END", 0))
		held := accessExecutionHeld(t, p, c, "SELECT "+function+"() AS v")
		for _, grant := range []bool{false, true, false} {
			verb, tail := "REVOKE ", " FROM og_object_reader"
			if grant {
				verb, tail = "GRANT ", " TO og_object_reader"
			}
			run(fixture.Exec(verb+o.privilege+" ON "+o.target+tail, 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(objectGrantAccessValue("SELECT v FROM "+view, grant))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(held.SQL, grant))
			})
		}
		// The executing invoker still needs EXECUTE on the selected declaration;
		// querying a superuser's rights cannot authorize invoking the inquiry.
		signature := "pg_catalog." + o.function + "(name,oid,text)"
		var acl sql.NullString
		if b.PG {
			if err := c.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1::regprocedure", signature).Scan(&acl); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := b.DB.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION "+signature+" TO PUBLIC"); err != nil {
				t.Error(err)
			}
			if b.PG {
				if _, err := b.DB.ExecContext(ctx, "UPDATE pg_proc SET proacl=$1::aclitem[] WHERE oid=$2::regprocedure", acl, signature); err != nil {
					t.Error(err)
				}
			}
		})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(accessExecutionError(fmt.Sprintf("SELECT %s('postgres'::name,%d::oid,'%s') AS v", o.function, o.oid, o.privilege), "42501", "permission denied for function "+o.function))
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO PUBLIC", 0))
	}
}
