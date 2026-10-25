package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterConstraintMetadata(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"domain_alter_catalog_reader"})
	domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.d AS integer CONSTRAINT upper_bound CHECK(VALUE<10)",
			"CREATE DOMAIN domain_s.child AS domain_s.d CONSTRAINT own_child CHECK(VALUE<>5)",
			"CREATE TABLE domain_s.t(v domain_s.d CONSTRAINT table_check CHECK(v<>9))",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.t VALUES(-1)", 1))
		run(fixture.Exec("ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", 0))
		columns := []string{"oid", "conname", "connamespace", "contype", "condeferrable", "condeferred", "conenforced", "convalidated", "conrelid", "contypid", "conindid", "conparentid", "confrelid", "confupdtype", "confdeltype", "confmatchtype", "conislocal", "coninhcount", "connoinherit", "conperiod", "conkey", "confkey", "conpfeqop", "conppeqop", "conffeqop", "confdelsetcols", "conexclop", "conbin"}
		types := []string{"OID", "NAME", "OID", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "OID", "OID", "OID", "OID", "OID", "CHAR", "CHAR", "CHAR", "BOOL", "INT2", "BOOL", "BOOL", "_INT2", "_INT2", "_OID", "_OID", "_OID", "_INT2", "_OID", "PG_NODE_TREE"}
		run(fixture.Select("SELECT * FROM pg_constraint WHERE false", columns, types, [][]any{}))
		domain := domainAlterOID(t, p, c, "SELECT 'domain_s.d'::regtype::oid")
		namespace := domainAlterOID(t, p, c, "SELECT oid FROM pg_namespace WHERE nspname='domain_s'")
		positive := domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid AND conname='positive'")
		upper := domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid AND conname='upper_bound'")
		if positive == upper || positive == domain || upper == domain {
			t.Fatal("domain/CHECK identities alias")
		}
		// Assert every field; only structural nodeToString bytes are deliberately
		// not equated across engines. The raw conbin column's public type is above.
		projection := "SELECT oid,conname,connamespace,contype,condeferrable,condeferred,conenforced,convalidated,conrelid,contypid,conindid,conparentid,confrelid,confupdtype,confdeltype,confmatchtype,conislocal,coninhcount,connoinherit,conperiod,conkey,confkey,conpfeqop,conppeqop,conffeqop,confdelsetcols,conexclop,conbin IS NOT NULL AS tree FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid ORDER BY conname"
		projectionColumns := append([]string(nil), columns...)
		projectionTypes := append([]string(nil), types...)
		projectionColumns[27], projectionTypes[27] = "tree", "BOOL"
		// The fixture preserves each backend's OID wire contract before
		// normalization: native int64, PostgreSQL text bytes normalized to string.
		// Only expected cell representations differ; SQL fields/types are intact.
		oidValue := func(id int64) any {
			if b.PG {
				return fmt.Sprint(id)
			}
			return id
		}
		row := func(id int64, name string, valid bool) []any {
			return []any{oidValue(id), name, oidValue(namespace), "c", false, false, true, valid, oidValue(0), oidValue(domain), oidValue(0), oidValue(0), oidValue(0), " ", " ", " ", true, int64(0), false, false, nil, nil, nil, nil, nil, nil, nil, true}
		}
		full := fixture.Select(projection, projectionColumns, projectionTypes, [][]any{row(positive, "positive", false), row(upper, "upper_bound", true)})
		run(full)
		run(fixture.Prepared(full))
		run(domainAlterFlags("child", [][]any{{"own_child", true}}))
		run(fixture.Select("SELECT conname,contype,convalidated,contypid=0::oid AS table_owned FROM pg_constraint WHERE conrelid='domain_s.t'::regclass::oid", []string{"conname", "contype", "convalidated", "table_owned"}, []string{"NAME", "CHAR", "BOOL", "BOOL"}, [][]any{{"table_check", "c", true, true}}))
		flagQuery := "SELECT conname,convalidated FROM pg_constraint WHERE contypid=$1::oid ORDER BY conname"
		stmt, err := c.PrepareContext(p.Ctx, flagQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: flagQuery}
		flags := fixture.Select(flagQuery, []string{"conname", "convalidated"}, []string{"NAME", "BOOL"}, [][]any{{"positive", false}, {"upper_bound", true}}, domain)
		fixture.RunStep(t, p.Ctx, held, r, flags)
		var retained *sql.Rows
		if !b.PG {
			retained, err = b.DB.QueryContext(p.Ctx, flagQuery, domain)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		// Independent rendering expectations follow ruleutils' compact/pretty
		// convention. Root must settle any REL_18_0 formatting differences first.
		for _, tc := range []struct{ suffix, expr, definition string }{
			{"", "(VALUE > 0)", "CHECK ((VALUE > 0)) NOT VALID"},
			{",false", "(VALUE > 0)", "CHECK ((VALUE > 0)) NOT VALID"},
			{",true", "VALUE > 0", "CHECK (VALUE > 0) NOT VALID"},
		} {
			q := "SELECT pg_get_constraintdef(oid" + tc.suffix + ") AS definition,pg_get_expr(conbin,0" + tc.suffix + ") AS expression FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid AND conname='positive'"
			s := fixture.Select(q, []string{"definition", "expression"}, []string{"TEXT", "TEXT"}, [][]any{{tc.definition, tc.expr}})
			t.Run("deparse"+tc.suffix, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, r, s)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
			})
		}
		t.Run("failed_validation", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, domainAlterOldFailure("ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive", "t", "v"))
		})
		fixture.RunStep(t, p.Ctx, held, r, flags)
		run(fixture.Exec("UPDATE domain_s.t SET v=1", 1))
		run(fixture.Exec("ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive", 0))
		flags.Rows = [][]any{{"positive", true}, {"upper_bound", true}}
		fixture.RunStep(t, p.Ctx, held, r, flags)
		full.Rows = [][]any{row(positive, "positive", true), row(upper, "upper_bound", true)}
		run(full)
		run(fixture.Select("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid AND conname='positive'", []string{"definition"}, []string{"TEXT"}, [][]any{{"CHECK ((VALUE > 0))"}}))
		if retained != nil {
			for _, name := range []string{"positive", "upper_bound"} {
				if !retained.Next() {
					t.Fatal("lost eager metadata row", retained.Err())
				}
				var got string
				var valid bool
				if err := retained.Scan(&got, &valid); err != nil {
					t.Fatal(err)
				}
				if got != name || valid != (name == "upper_bound") {
					t.Fatalf("retained metadata %s,%v", got, valid)
				}
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained metadata trailing/error", retained.Err())
			}
			retained.Close()
		}
		for _, q := range []string{"CREATE ROLE domain_alter_catalog_reader", "SET ROLE domain_alter_catalog_reader"} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		fixture.RunStep(t, p.Ctx, held, r, flags)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER DOMAIN domain_s.d DROP CONSTRAINT positive", 0))
		stale := fixture.Select(fmt.Sprintf("SELECT pg_get_constraintdef(%d::oid) AS definition", positive), []string{"definition"}, []string{"TEXT"}, [][]any{{nil}})
		run(stale)
		run(fixture.Prepared(stale))
		run(domainAlterFlags("d", [][]any{{"upper_bound", true}}))
	})
}
