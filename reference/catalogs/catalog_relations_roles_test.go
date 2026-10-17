package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCatalogRelationsRoles(t *testing.T) {
	names := []string{"cr_roles_login", "cr_roles_nologin", "Cr odd é\"role"}
	fixture.RoleReferenceCleanup(t, names)
	p := fixture.NewScenario(t)
	catalogRelationsBackends(t, p, func(t *testing.T, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE cr_roles_login LOGIN", 0))
		run(fixture.Exec("CREATE ROLE cr_roles_nologin NOLOGIN", 0))
		run(fixture.Exec(`CREATE ROLE "Cr odd é""role" NOLOGIN`, 0))
		for _, name := range names {
			oid := catalogRelationsRoleOID(t, p, conn, name)
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT pg_catalog.pg_get_userbyid($1) AS n", []string{"n"}, []string{"NAME"}, [][]any{{name}}, oid)
				s.Prepared = prepared
				s.CheckMetadata = catalogRelationsMetadata(b.PG)
				run(s)
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT pg_get_userbyid(oid)=rolname AS same FROM pg_catalog.pg_roles WHERE rolname=current_user", []string{"same"}, []string{"BOOL"}, [][]any{{true}})
			s.Prepared = prepared
			run(s)
			s = fixture.Select("SELECT pg_get_userbyid(0) AS zero, pg_get_userbyid('4294967295') AS missing, pg_get_userbyid(NULL) AS untyped, pg_get_userbyid(NULL::oid) AS typed", []string{"zero", "missing", "untyped", "typed"}, []string{"NAME", "NAME", "NAME", "NAME"}, [][]any{{"unknown (OID=0)", "unknown (OID=4294967295)", nil, nil}})
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_roles WHERE oid='4294967295'::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		for _, cast := range []string{"smallint", "integer", "bigint", "oid", "regclass", "regtype", "regrole"} {
			run(fixture.Select(fmt.Sprintf("SELECT pg_get_userbyid(0::%s) AS n", cast), []string{"n"}, []string{"NAME"}, [][]any{{"unknown (OID=0)"}}))
		}
		heldSQL := "SELECT pg_get_userbyid($1) AS n"
		stmt, err := conn.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: heldSQL}
		oid := catalogRelationsRoleOID(t, p, conn, names[1])
		for _, sample := range []struct {
			arg  any
			want any
		}{{nil, nil}, {oid, names[1]}, {int64(0), "unknown (OID=0)"}, {oid, names[1]}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(heldSQL, []string{"n"}, []string{"NAME"}, [][]any{{sample.want}}, sample.arg))
		}
		run(fixture.Exec("SET ROLE cr_roles_nologin", 0))
		defer conn.ExecContext(p.Ctx, "RESET ROLE")
		run(fixture.Select("SELECT pg_get_userbyid(oid)=rolname AS same FROM pg_catalog.pg_roles WHERE rolname IN ('cr_roles_login','cr_roles_nologin') ORDER BY rolname", []string{"same"}, []string{"BOOL"}, [][]any{{true}, {true}}))
		run(fixture.Select("SELECT pg_get_userbyid(oid)=current_user AS same FROM pg_catalog.pg_roles WHERE rolname=current_user", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("RESET ROLE", 0))
	})
}

func TestReferenceCatalogRelationsHeld(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"cr_held_role", "cr_held_rollback"})
	p := fixture.NewScenario(t)
	catalogRelationsBackends(t, p, func(t *testing.T, b fixture.Backend) { catalogRelationsHeld(t, p, b) })
}

func catalogRelationsHeld(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	query := "SELECT pg_get_userbyid($1) AS n"
	statement, err := conn.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	held := fixture.HeldStatement{Executor: conn, Statement: statement, SQL: query}
	lookup := func(oid int64, name string) {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(held.SQL, []string{"n"}, []string{"NAME"}, [][]any{{name}}, oid))
	}
	run(fixture.Exec("CREATE ROLE cr_held_role NOLOGIN", 0))
	old := catalogRelationsRoleOID(t, p, b.DB, "cr_held_role")
	lookup(old, "cr_held_role")
	// A separate physical borrower avoids pq's same-connection streaming protocol.
	// Native eagerly retained rows must retain the old NAME after later publication.
	var retainedClose func()
	if !b.PG {
		rows, err := held.Statement.QueryContext(p.Ctx, old)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		retainedClose = func() {
			columns, err := rows.ColumnTypes()
			if err != nil || len(columns) != 1 || columns[0].DatabaseTypeName() != "NAME" {
				t.Fatal(columns, err)
			}
			if !rows.Next() {
				t.Fatal("missing retained name", rows.Err())
			}
			var got string
			if err := rows.Scan(&got); err != nil || got != "cr_held_role" {
				t.Fatal(got, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra retained rows", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	run(fixture.Exec("DROP ROLE cr_held_role", 0))
	lookup(old, fmt.Sprintf("unknown (OID=%d)", old))
	run(fixture.Exec("CREATE ROLE cr_held_role NOLOGIN", 0))
	fresh := catalogRelationsRoleOID(t, p, b.DB, "cr_held_role")
	if fresh == old {
		t.Fatal("recreated role reused dropped identity", old)
	}
	lookup(fresh, "cr_held_role")
	lookup(old, fmt.Sprintf("unknown (OID=%d)", old))
	if retainedClose != nil {
		retainedClose()
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("BEGIN", 0))
	defer conn.ExecContext(p.Ctx, "ROLLBACK")
	utility := "CREATE ROLE cr_held_rollback NOLOGIN"
	if b.PG {
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(utility, 0))
		rolled := catalogRelationsRoleOID(t, p, conn, "cr_held_rollback")
		lookup(rolled, "cr_held_rollback")
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("ROLLBACK", 0))
		lookup(rolled, fmt.Sprintf("unknown (OID=%d)", rolled))
	} else {
		s := fixture.ImplicitError(utility, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("ROLLBACK", 0))
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_roles WHERE rolname='cr_held_rollback'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	lookup(fresh, "cr_held_role")
	// Both backends support transactional effective-role changes. Retain the
	// SAME statement and physical owner across SET ROLE and its real rollback.
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("BEGIN", 0))
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET ROLE cr_held_role", 0))
	defer conn.ExecContext(p.Ctx, "RESET ROLE")
	lookup(fresh, "cr_held_role")
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT pg_get_userbyid($1)=current_user AS same", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, fresh))
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("ROLLBACK", 0))
	lookup(fresh, "cr_held_role")
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT pg_get_userbyid(oid)=current_user AND current_user=session_user AS same FROM pg_catalog.pg_roles WHERE rolname=current_user", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
}
