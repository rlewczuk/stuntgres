package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const pgrowMissingHint = "No function matches the given name and argument types. You might need to add explicit type casts."

func TestReferencePgrowlocksInstalledAndShape(t *testing.T) {
	pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		q := "SELECT * FROM pgrowlocks('pgrow_t')"
		pgrowError(t, p, observer, q, "prepare", fixture.Diagnostic{Code: "42883", Message: "function pgrowlocks(unknown) does not exist", Hint: pgrowMissingHint, Position: pgrowPosition(q, "pgrowlocks")})
		pgrowInstall(t, p, a)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE pgrow_t(id INT PRIMARY KEY,v INT)", 0)
		pgrowEmpty(t, p, observer)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO pgrow_t VALUES(1,0)", 1)
		pgrowEmpty(t, p, observer)
		identityBoth(t, p, b, observer, fixture.Select(`SELECT e.extversion,e.extrelocatable,e.extowner=r.oid AS owned,n.nspname
FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.rolname=current_user WHERE e.extname='pgrowlocks'`,
			[]string{"extversion", "extrelocatable", "owned", "nspname"}, []string{"TEXT", "BOOL", "BOOL", "NAME"}, [][]any{{"1.2", true, true, "public"}}))
		// Native pg_proc retains its established nine-column projection.
		// The shared cursor tests below prove the six OUT fields and U4 checks
		// the actual installed provider/record shape. Full release declaration
		// metadata is corroborated on PostgreSQL, not invented catalog columns.
		proc := fixture.Select(`SELECT p.proname,p.pronargs,p.prorettype::oid,p.proisstrict,p.provolatile,p.proparallel,p.proowner=e.extowner AS owned,p.pronamespace=e.extnamespace AS located
FROM pg_proc p JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e'
JOIN pg_extension e ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid WHERE e.extname='pgrowlocks'`,
			[]string{"proname", "pronargs", "prorettype", "proisstrict", "provolatile", "proparallel", "owned", "located"},
			[]string{"NAME", "INT2", "OID", "BOOL", "CHAR", "CHAR", "BOOL", "BOOL"}, [][]any{{"pgrowlocks", int64(1), int64(2249), true, "v", "s", true, true}})
		for _, prepared := range []bool{false, true} {
			proc.Prepared = prepared
			fixture.OidReferenceRows(t, p.Ctx, b, observer, proc)
		}
		if b.PG {
			identityBoth(t, p, b, observer, fixture.Select(`SELECT proretset FROM pg_proc WHERE oid='pgrowlocks(text)'::regprocedure`, []string{"proretset"}, []string{"BOOL"}, [][]any{{true}}))
			identityBoth(t, p, b, observer, fixture.Select(`SELECT proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text FROM pg_proc WHERE oid='pgrowlocks(text)'::regprocedure`,
				[]string{"proargtypes", "proallargtypes", "proargmodes", "proargnames"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"},
				[][]any{{"25", "{25,27,28,16,1011,1009,1007}", "{i,o,o,o,o,o,o}", "{relname,locked_row,locker,multi,xids,modes,pids}"}}))
		}
		for _, q := range []string{"SELECT * FROM pgrowlocks(NULL)", "SELECT * FROM pgrowlocks(relname => NULL::text)"} {
			if got := pgrowQuery(t, p, observer, q); len(got) != 0 {
				t.Fatal("strict NULL", got)
			}
		}
		stmt, err := observer.PrepareContext(p.Ctx, "SELECT * FROM pgrowlocks($1)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, arg := range []any{nil, "pgrow_t"} {
			rows, err := stmt.QueryContext(p.Ctx, arg)
			if err != nil {
				t.Fatal(err)
			}
			if got := pgrowRead(t, rows); len(got) != 0 {
				t.Fatal(got)
			}
		}
		fixture.LockExec(t, p.Ctx, a, `CREATE TABLE "123"(id INT)`, 0)
		if got := pgrowQuery(t, p, observer, "SELECT * FROM pgrowlocks('123')"); len(got) != 0 {
			t.Fatal("numeric TEXT table name", got)
		}
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		owner := pgrowOwner(t, p, a, "For Update")
		identityLock(t, p, a, "pgrow_t", 1, "UPDATE")
		for _, q := range []string{"SELECT * FROM pgrowlocks('pgrow_t')", "SELECT * FROM pgrowlocks(relname => 'public.pgrow_t'::varchar)"} {
			pgrowOne(t, pgrowQuery(t, p, observer, q), false, owner)
		}
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
	})
}

func TestReferencePgrowlocksExpressionRoutes(t *testing.T) {
	pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		pgrowTable(t, p, a)
		stmt, err := observer.PrepareContext(p.Ctx, "SELECT * FROM pgrowlocks($1::text)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		rows, err := stmt.QueryContext(p.Ctx, "pgrow_t")
		if err != nil {
			t.Fatal(err)
		}
		if got := pgrowRead(t, rows); len(got) != 0 {
			t.Fatal(got)
		}
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		owner := pgrowOwner(t, p, a, "For Update")
		identityLock(t, p, a, "pgrow_t", 1, "UPDATE")
		first := pgrowOne(t, pgrowGet(t, p, observer), false, owner)
		routes := []string{
			"SELECT r.* FROM public.pgrowlocks(relname => 'pgrow_t') AS r",
			"SELECT (pgrowlocks('pgrow_t')).*",
			"SELECT (pgrowlocks('pgrow_t')).locked_row,(pgrowlocks('pgrow_t')).locker,(pgrowlocks('pgrow_t')).multi,(pgrowlocks('pgrow_t')).xids,(pgrowlocks('pgrow_t')).modes,(pgrowlocks('pgrow_t')).pids",
			"SELECT r.* FROM (VALUES('pgrow_t'::text)) AS names(n) CROSS JOIN LATERAL pgrowlocks(names.n) AS r",
			"SELECT a AS locked_row,b AS locker,c AS multi,d AS xids,e AS modes,f AS pids FROM pgrowlocks('pgrow_t') AS r(a,b,c,d,e,f)",
		}
		for _, q := range routes {
			for _, prepared := range []bool{false, true} {
				out := fixture.RowQuery(p.Ctx, observer, q, prepared)
				if out.Err != nil {
					t.Fatal(q, out.Err)
				}
				pgrowSameTID(t, first, pgrowOne(t, pgrowRead(t, out.Rows), false, owner))
				if out.Stmt != nil {
					out.Stmt.Close()
				}
			}
		}
		identityBoth(t, p, b, observer, fixture.Select("SELECT pgrowlocks('pgrow_t') IS NOT NULL AS present", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
		identityBoth(t, p, b, observer, fixture.Select("SELECT ordinality FROM pgrowlocks('pgrow_t') WITH ORDINALITY", []string{"ordinality"}, []string{"INT8"}, [][]any{{int64(1)}}))
		rows, err = stmt.QueryContext(p.Ctx, "pgrow_t")
		if err != nil {
			t.Fatal(err)
		}
		// Small materialized result is owned even after its lock owner ends.
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		pgrowSameTID(t, first, pgrowOne(t, pgrowRead(t, rows), false, owner))
		rows, err = stmt.QueryContext(p.Ctx, "pgrow_t")
		if err != nil {
			t.Fatal(err)
		}
		if got := pgrowRead(t, rows); len(got) != 0 {
			t.Fatal("held execution cached locks", got)
		}
		for _, q := range routes {
			for _, prepared := range []bool{false, true} {
				out := fixture.RowQuery(p.Ctx, observer, q, prepared)
				if out.Err != nil {
					t.Fatal(q, out.Err)
				}
				if got := pgrowRead(t, out.Rows); len(got) != 0 {
					t.Fatal("empty route retained lock output", q, got)
				}
				if out.Stmt != nil {
					out.Stmt.Close()
				}
			}
		}
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		owner = pgrowOwner(t, p, a, "For Share")
		identityLock(t, p, a, "pgrow_t", 1, "SHARE")
		rows, err = stmt.QueryContext(p.Ctx, "pgrow_t")
		if err != nil {
			t.Fatal(err)
		}
		pgrowSameTID(t, first, pgrowOne(t, pgrowRead(t, rows), false, owner))
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE pgrow_evaluations", 0)
		for n := int64(1); n <= 2; n++ {
			if got := pgrowQuery(t, p, observer, "SELECT * FROM pgrowlocks(CASE WHEN nextval('pgrow_evaluations')>0 THEN 'pgrow_t' ELSE 'missing' END)"); len(got) != 0 {
				t.Fatal(got)
			}
			identityBoth(t, p, b, observer, fixture.Select("SELECT currval('pgrow_evaluations') AS n", []string{"n"}, []string{"INT8"}, [][]any{{n}}))
		}
		for _, tc := range []struct{ q, code, message, token, hint string }{
			{"SELECT pgrowlocks()", "42883", "function pgrowlocks() does not exist", "pgrowlocks", pgrowMissingHint},
			{"SELECT pgrowlocks(1)", "42883", "function pgrowlocks(integer) does not exist", "pgrowlocks", pgrowMissingHint},
			{"SELECT pgrowlocks('a','b')", "42883", "function pgrowlocks(unknown, unknown) does not exist", "pgrowlocks", pgrowMissingHint},
			{"SELECT pgrowlocks(wrong => 'pgrow_t')", "42883", "function pgrowlocks(wrong => unknown) does not exist", "pgrowlocks", pgrowMissingHint},
			{"SELECT pgrowlocks(DISTINCT 'pgrow_t')", "42809", "DISTINCT specified, but pgrowlocks is not an aggregate function", "pgrowlocks", ""},
			{"SELECT * FROM pgrow_t WHERE pgrowlocks('pgrow_t') IS NULL", "0A000", "set-returning functions are not allowed in WHERE", "pgrowlocks", ""},
			{"SELECT 'é',pgrowlocks(1)", "42883", "function pgrowlocks(integer) does not exist", "pgrowlocks", pgrowMissingHint},
		} {
			t.Run(tc.q, func(t *testing.T) {
				pgrowError(t, p, observer, tc.q, "prepare", fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: pgrowPosition(tc.q, tc.token), Hint: tc.hint})
			})
		}
	})
}
