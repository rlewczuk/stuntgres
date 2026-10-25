package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDomainDefaultMetadata(t *testing.T) {
	domainDefaultScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.mi AS integer DEFAULT 7 NOT NULL",
			"CREATE DOMAIN domain_s.mc AS domain_s.mi NULL",
			"CREATE DOMAIN domain_s.mn AS numeric(5,2) DEFAULT 12.345",
			"CREATE DOMAIN domain_s.mv AS varchar(3) DEFAULT 'abc'",
			"CREATE DOMAIN domain_s.mm AS domain_s.mn DEFAULT NULL",
			"CREATE TABLE domain_s.default_meta(i domain_s.mi,n domain_s.mn,v domain_s.mv)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.default_meta DEFAULT VALUES", 1))
		for _, prepared := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				q := "SELECT i,n,v FROM domain_s.default_meta"
				want := [][]any{{int64(7), "12.35", "abc"}}
				if empty {
					q += " WHERE false"
					want = nil
				}
				s := fixture.Select(q, []string{"i", "n", "v"}, []string{"INT4", "NUMERIC", "VARCHAR"}, want)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					domainReferenceCheckMetadata(t, rows, b.PG, false, s.Types)
				}
				run(s)
			}
		}
		run(fixture.Select("SELECT t.typname AS name,t.typnotnull AS required FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typname IN ('mi','mc','mn','mv','mm') ORDER BY t.typname", []string{"name", "required"}, []string{"NAME", "BOOL"}, [][]any{{"mc", false}, {"mi", true}, {"mm", false}, {"mn", false}, {"mv", false}}))
		// Native pg_node_tree is the existing boundTree structural format, not
		// PostgreSQL nodeToString. Each backend asserts its own concrete root kind.
		structural := "CASE WHEN t.typname IN ('mc','mi') THEN t.typdefaultbin::text LIKE '{type 23 typmod -1 collation 0 derivation 0 node Const value \"7\"}' ELSE t.typdefaultbin::text LIKE '{type % node Coerce mode % {type %}' END"
		if b.PG {
			structural = "CASE WHEN t.typname IN ('mc','mi') THEN t.typdefaultbin::text LIKE '{CONST %' WHEN t.typname='mm' THEN t.typdefaultbin::text LIKE '{COERCETODOMAIN %' ELSE t.typdefaultbin::text LIKE '{FUNCEXPR %' END"
		}
		run(fixture.Select("SELECT t.typname AS name,t.typdefault,t.typdefaultbin IS NOT NULL AS tree_present,("+structural+") AS structural FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typtype='d' ORDER BY t.typname", []string{"name", "typdefault", "tree_present", "structural"}, []string{"NAME", "TEXT", "BOOL", "BOOL"}, [][]any{{"mc", "7", true, true}, {"mi", "7", true, true}, {"mm", "NULL::numeric", true, true}, {"mn", "12.345", true, true}, {"mv", "'abc'::character varying", true, true}}))
	})
}

func TestReferenceDomainDefaultDependencies(t *testing.T) {
	domainDefaultScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE SEQUENCE domain_s.default_provider START 10",
			"CREATE DOMAIN domain_s.dependent AS bigint DEFAULT nextval('domain_s.default_provider'::regclass)",
			"CREATE DOMAIN domain_s.inherited_dependent AS domain_s.dependent",
			"CREATE TABLE domain_s.dependent_rows(id integer,v domain_s.inherited_dependent)",
			"CREATE VIEW domain_s.dependent_view AS SELECT id,v FROM domain_s.dependent_rows",
		} {
			run(fixture.Exec(q, 0))
		}
		q := "INSERT INTO domain_s.dependent_rows(id) VALUES($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, r, fixture.Exec(q, 1, int64(1)))
		d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop sequence domain_s.default_provider because other objects depend on it")
		d.Detail = "type domain_s.dependent depends on sequence domain_s.default_provider\ntype domain_s.inherited_dependent depends on sequence domain_s.default_provider\ncolumn v of table domain_s.dependent_rows depends on type domain_s.inherited_dependent\nview domain_s.dependent_view depends on column v of table domain_s.dependent_rows"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(domainDefaultFailure("DROP SEQUENCE domain_s.default_provider RESTRICT", d, false, false))
		fixture.RunStep(t, p.Ctx, held, r, fixture.Exec(q, 1, int64(2)))
		run(fixture.Select("SELECT id,v FROM domain_s.dependent_view ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(11)}}))
		run(fixture.Exec("DROP SEQUENCE domain_s.default_provider CASCADE", 0))
		run(fixture.Select("SELECT t.typname AS name,t.typdefault FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typtype='d' ORDER BY t.typname", []string{"name", "typdefault"}, []string{"NAME", "TEXT"}, nil))
		d = fixture.ControlsReferenceDiagnostic("42P01", `relation "domain_s.dependent_view" does not exist`)
		d.Position = "18"
		run(domainDefaultFailure("SELECT id,v FROM domain_s.dependent_view ORDER BY id", d, false, true))
		fixture.RunStep(t, p.Ctx, held, r, fixture.Exec(q, 1, int64(3)))
		run(fixture.Select("SELECT id FROM domain_s.dependent_rows ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		d = fixture.ControlsReferenceDiagnostic("42703", `column "v" does not exist`)
		d.Position = "11"
		run(domainDefaultFailure("SELECT id,v FROM domain_s.dependent_rows ORDER BY id", d, false, true))
		for _, q := range []string{
			"CREATE DOMAIN domain_s.cast_provider AS integer CHECK(VALUE>0)",
			"CREATE DOMAIN domain_s.cast_consumer AS integer DEFAULT 7::domain_s.cast_provider",
			"CREATE TABLE domain_s.cast_rows(v domain_s.cast_consumer)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.cast_rows DEFAULT VALUES", 1))
		run(fixture.Select("SELECT v FROM domain_s.cast_rows", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		d = fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type domain_s.cast_provider because other objects depend on it")
		d.Detail = "type domain_s.cast_consumer depends on type domain_s.cast_provider\ncolumn v of table domain_s.cast_rows depends on type domain_s.cast_consumer"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(domainDefaultFailure("DROP DOMAIN domain_s.cast_provider RESTRICT", d, false, false))
		run(fixture.Exec("DROP DOMAIN domain_s.cast_provider CASCADE", 0))
		run(fixture.Select("SELECT t.typname AS name,t.typdefault FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typtype='d' ORDER BY t.typname", []string{"name", "typdefault"}, []string{"NAME", "TEXT"}, nil))
		d = fixture.ControlsReferenceDiagnostic("42703", `column "v" does not exist`)
		d.Position = "8"
		run(domainDefaultFailure("SELECT v FROM domain_s.cast_rows", d, false, true))
	})
}
