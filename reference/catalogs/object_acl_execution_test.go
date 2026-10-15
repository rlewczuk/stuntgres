package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectACLMetadata(t *testing.T) {
	objectACLScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { objectACLRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE acl_obj_metadata(a INT)", 0))
		for _, tc := range []struct {
			query string
			rows  [][]any
			typ   string
		}{
			{"SELECT pg_get_acl(1259,'acl_obj_metadata'::regclass,1) AS acl", [][]any{{nil}}, "_ACLITEM"},
			{"SELECT pg_get_acl(NULL,NULL,NULL) AS acl", [][]any{{nil}}, "_ACLITEM"},
			{"SELECT pg_get_acl(1259,0,0) AS acl WHERE FALSE", nil, "_ACLITEM"},
			{"SELECT unnest(pg_get_acl(1259,0,0)) AS acl", nil, "ACLITEM"},
		} {
			s := fixture.Select(tc.query, []string{"acl"}, []string{tc.typ}, tc.rows)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { objectACLMetadata(t, rows, b.PG, tc.typ) }
			run(s)
		}
		run(fixture.Exec("GRANT SELECT(a) ON acl_obj_metadata TO PUBLIC", 0))
		for _, scalar := range []bool{false, true} {
			q, typ, value := "SELECT pg_get_acl(1259,'acl_obj_metadata'::regclass,1) AS acl", "_ACLITEM", "{=r/"+objectACLOwner(b)+"}"
			if scalar {
				q, typ, value = "SELECT unnest(pg_get_acl(1259,'acl_obj_metadata'::regclass,1)) AS acl", "ACLITEM", "=r/"+objectACLOwner(b)
			}
			s := fixture.Select(q, []string{"acl"}, []string{typ}, [][]any{{value}})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { objectACLMetadata(t, rows, b.PG, typ) }
			// RunStep checks raw cells and middleware-owned raw rows before
			// normalization; the literal above is the canonical public spelling.
			run(s)
		}
		run(fixture.Exec("REVOKE ALL ON acl_obj_metadata FROM "+objectACLOwner(b), 0))
		s := objectACLSelect("SELECT pg_get_acl(1259,'acl_obj_metadata'::regclass,0) AS acl", "{}")
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { objectACLMetadata(t, rows, b.PG, "_ACLITEM") }
		run(s)
	})
}

func TestReferenceObjectACLHeldExecution(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"acl_obj_held_renamed"})
	objectACLScenario(t, []string{"acl_obj_held_reader", "acl_obj_held_observer"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { objectACLRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE acl_obj_held(a INT,b INT)", 0))
		run(fixture.Exec("INSERT INTO acl_obj_held VALUES (7,9)", 1))
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'acl_obj_held'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		q := "SELECT pg_get_acl($1,$2,$3) AS acl"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		check := func(subid int64, value any) {
			t.Helper()
			s := objectACLSelect(q, value)
			s.Args = []any{int64(1259), oid, subid}
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { objectACLMetadata(t, rows, b.PG, "_ACLITEM") }
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		check(1, nil)
		run(fixture.Exec("GRANT SELECT(a) ON acl_obj_held TO PUBLIC", 0))
		check(1, "{=r/"+objectACLOwner(b)+"}")
		run(fixture.Exec("REVOKE SELECT(a) ON acl_obj_held FROM PUBLIC", 0))
		check(1, nil)
		run(fixture.Exec("GRANT SELECT(a) ON acl_obj_held TO acl_obj_held_reader", 0))
		check(1, "{acl_obj_held_reader=r/"+objectACLOwner(b)+"}")
		// A raw numeric address is readable without table SELECT privileges.
		// Fixture resets leave public private to its owner. Give the observer
		// schema visibility without granting SELECT or changing the table owner.
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO acl_obj_held_observer", 0))
		} else {
			run(fixture.Exec("ALTER SCHEMA public OWNER TO acl_obj_held_observer", 0))
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "ALTER SCHEMA public OWNER TO postgres"); err != nil {
					t.Error("restore native public owner", err)
				}
			}()
		}
		run(fixture.Exec("SET ROLE acl_obj_held_observer", 0))
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		denied := fixture.ImplicitError("SELECT a FROM acl_obj_held", fixture.Diagnostic{Code: "42501", Message: "permission denied for table acl_obj_held"})
		denied.SelectRows = true
		fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
		check(1, "{acl_obj_held_reader=r/"+objectACLOwner(b)+"}")
		run(fixture.Exec("RESET ROLE", 0))
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		old, err := stmt.QueryContext(p.Ctx, int64(1259), oid, int64(1))
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		objectACLMetadata(t, old, b.PG, "_ACLITEM")
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("REVOKE SELECT(a) ON acl_obj_held FROM acl_obj_held_reader", 0))
		fixture.ScalarRows(t, b.PG, old, []string{"acl"}, []string{"_ACLITEM"}, [][]any{{"{acl_obj_held_reader=r/" + objectACLOwner(b) + "}"}}, false)
		fixture.ScalarAssertDescription(t, metadata, []string{"acl"}, []string{"_ACLITEM"})
		check(1, nil)
		run(fixture.Exec("GRANT SELECT(a) ON acl_obj_held TO acl_obj_held_reader", 0))
		beforeRename, err := stmt.QueryContext(p.Ctx, int64(1259), oid, int64(1))
		if err != nil {
			t.Fatal(err)
		}
		defer beforeRename.Close()
		objectACLMetadata(t, beforeRename, b.PG, "_ACLITEM")
		reader := "acl_obj_held_reader"
		const renameRole = "ALTER ROLE acl_obj_held_reader RENAME TO acl_obj_held_renamed"
		if b.PG {
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(renameRole, 0))
			reader = "acl_obj_held_renamed"
		} else {
			// Existing role-rename SQL is outside this selected inquiry.
			s := fixture.ImplicitError(renameRole, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported", Position: "1"})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Prepared(s))
		}
		fixture.ScalarRows(t, b.PG, beforeRename, []string{"acl"}, []string{"_ACLITEM"}, [][]any{{"{acl_obj_held_reader=r/" + objectACLOwner(b) + "}"}}, false)
		check(1, "{"+reader+"=r/"+objectACLOwner(b)+"}")
		run(fixture.Exec("REVOKE SELECT(a) ON acl_obj_held FROM "+reader, 0))
		check(1, nil)
		run(fixture.Exec("GRANT SELECT(a) ON acl_obj_held TO PUBLIC", 0))
		run(fixture.Exec("ALTER TABLE acl_obj_held RENAME COLUMN a TO old_a", 0))
		check(1, "{=r/"+objectACLOwner(b)+"}")
		run(fixture.Exec("ALTER TABLE acl_obj_held DROP COLUMN old_a", 0))
		run(fixture.Exec("ALTER TABLE acl_obj_held ADD COLUMN a INT", 0))
		check(1, nil)
		check(3, nil)
		// Named written order is independent of the formal slot order.
		namedQ := "SELECT pg_get_acl(objsubid => $1,objid => $2,classid => $3) AS acl"
		named, err := c.PrepareContext(p.Ctx, namedQ)
		if err != nil {
			t.Fatal(err)
		}
		defer named.Close()
		namedHeld := fixture.HeldStatement{Executor: c, Statement: named, SQL: namedQ}
		for _, value := range []any{nil, "{=r/" + objectACLOwner(b) + "}", nil} {
			if value != nil {
				run(fixture.Exec("GRANT SELECT(b) ON acl_obj_held TO PUBLIC", 0))
			} else {
				run(fixture.Exec("REVOKE SELECT(b) ON acl_obj_held FROM PUBLIC", 0))
			}
			s := objectACLSelect(namedQ, value)
			s.Args = []any{int64(2), oid, int64(1259)}
			fixture.RunStep(t, p.Ctx, namedHeld, b.Recorder, s)
		}
	})
}
