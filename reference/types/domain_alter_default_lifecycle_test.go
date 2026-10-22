package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterDefaultLifecycle(t *testing.T) {
	domainAlterValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.p AS integer DEFAULT 7",
			"CREATE DOMAIN domain_s.c AS domain_s.p",
			"CREATE DOMAIN domain_s.z AS domain_s.p",
			"ALTER DOMAIN domain_s.z DROP DEFAULT",
			"CREATE TABLE domain_s.t(id integer,p domain_s.p,c domain_s.c,z domain_s.z,o domain_s.p DEFAULT 99)",
		} {
			run(fixture.Exec(q, 0))
		}
		domainID := domainAlterOID(t, p, c, "SELECT 'domain_s.p'::regtype::oid")
		arrayID := domainAlterOID(t, p, c, "SELECT 'domain_s.p[]'::regtype::oid")
		run(fixture.Exec("INSERT INTO domain_s.t(id) VALUES(1)", 1))
		run(fixture.Prepared(fixture.Exec("ALTER DOMAIN domain_s.p SET DEFAULT 11", 0)))
		run(fixture.Exec("INSERT INTO domain_s.t(id) VALUES(2)", 1))
		run(fixture.Exec("CREATE DOMAIN domain_s.late AS domain_s.p", 0))
		run(fixture.Exec("CREATE TABLE domain_s.late_rows(v domain_s.late)", 0))
		run(fixture.Exec("INSERT INTO domain_s.late_rows DEFAULT VALUES", 1))
		run(fixture.Select("SELECT v FROM domain_s.late_rows", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
		run(fixture.Exec("ALTER DOMAIN domain_s.c DROP DEFAULT", 0))
		run(fixture.Exec("INSERT INTO domain_s.t(id) VALUES(3)", 1))
		run(fixture.Exec("ALTER DOMAIN domain_s.p DROP DEFAULT", 0))
		run(fixture.Prepared(fixture.Exec("ALTER DOMAIN domain_s.p DROP DEFAULT", 0)))
		run(fixture.Exec("INSERT INTO domain_s.t(id) VALUES(4)", 1))
		run(fixture.Exec("INSERT INTO domain_s.t VALUES(5,42,NULL,NULL,42)", 1))
		s := fixture.Select("SELECT id,p,c,z,o FROM domain_s.t ORDER BY id", []string{"id", "p", "c", "z", "o"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{
			{int64(1), int64(7), int64(7), nil, int64(99)},
			{int64(2), int64(11), int64(7), nil, int64(99)},
			{int64(3), int64(11), nil, nil, int64(99)},
			{int64(4), nil, nil, nil, int64(99)},
			{int64(5), int64(42), nil, nil, int64(42)},
		})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, b.PG, false, s.Types) }
		run(s)
		run(fixture.Select("SELECT typname,typdefault,typdefaultbin IS NOT NULL AS recipe FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typtype='d' ORDER BY typname", []string{"typname", "typdefault", "recipe"}, []string{"NAME", "TEXT", "BOOL"}, [][]any{{"c", nil, false}, {"late", "11", true}, {"p", nil, false}, {"z", nil, false}}))
		run(fixture.Select("SELECT count(*) AS n FROM pg_attrdef WHERE oid='domain_s.p'::regtype::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if domainAlterOID(t, p, c, "SELECT 'domain_s.p'::regtype::oid") != domainID || domainAlterOID(t, p, c, "SELECT 'domain_s.p[]'::regtype::oid") != arrayID {
			t.Fatal("default alteration changed logical type identity")
		}
		// Default provider edges are replaced, not accumulated or copied from
		// another domain's present-day recipe.
		for _, q := range []string{"CREATE SEQUENCE domain_s.a", "CREATE SEQUENCE domain_s.b", "CREATE DOMAIN domain_s.seq AS bigint DEFAULT nextval('domain_s.a'::regclass)", "ALTER DOMAIN domain_s.seq SET DEFAULT nextval('domain_s.b'::regclass)", "DROP SEQUENCE domain_s.a"} {
			run(fixture.Exec(q, 0))
		}
		d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop sequence domain_s.b because other objects depend on it")
		d.Detail = "type domain_s.seq depends on sequence domain_s.b"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		t.Run("provider_dependency", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("DROP SEQUENCE domain_s.b", d))
		})
		run(fixture.Exec("ALTER DOMAIN domain_s.seq DROP DEFAULT", 0))
		run(fixture.Exec("DROP SEQUENCE domain_s.b", 0))
	})
}

func TestReferenceDomainAlterDefaultPrepared(t *testing.T) {
	domainAlterValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{"CREATE DOMAIN domain_s.p AS integer DEFAULT 7", "CREATE DOMAIN domain_s.c AS domain_s.p", "CREATE TABLE domain_s.t(id integer,p domain_s.p,c domain_s.c)", "PREPARE domain_default_insert(integer) AS INSERT INTO domain_s.t(id) VALUES($1)"} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "DEALLOCATE domain_default_insert")
		insert := domainAlterHold(t, p, c, "INSERT INTO domain_s.t(id) VALUES($1)")
		selectHeld := domainAlterHold(t, p, c, "SELECT id,p,c FROM domain_s.t ORDER BY id")
		data := fixture.Select(selectHeld.SQL, []string{"id", "p", "c"}, []string{"INT4", "INT4", "INT4"}, nil)
		data.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, b.PG, false, data.Types) }
		metadata := func(parent any) {
			run(fixture.Select("SELECT typname,typdefault,typdefaultbin IS NOT NULL AS recipe FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND typname IN('p','c') ORDER BY typname", []string{"typname", "typdefault", "recipe"}, []string{"NAME", "TEXT", "BOOL"}, [][]any{{"c", "7", true}, {"p", parent, parent != nil}}))
		}
		metadata("7")
		fixture.RunStep(t, p.Ctx, selectHeld, r, data)
		fixture.RunStep(t, p.Ctx, insert, r, fixture.Exec(insert.SQL, 1, 1))
		run(fixture.Exec("ALTER DOMAIN domain_s.p SET DEFAULT 11", 0))
		metadata("11")
		// These fresh analysis routes are distinct from the SAME held Go and
		// SQL PREPARE recipes below. Do not replace the held witnesses.
		run(fixture.Exec("INSERT INTO domain_s.t(id) VALUES(6)", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO domain_s.t(id) VALUES(7)", 1)))
		run(fixture.Exec("PREPARE domain_default_after_set(integer) AS INSERT INTO domain_s.t(id) VALUES($1)", 0))
		defer c.ExecContext(p.Ctx, "DEALLOCATE domain_default_after_set")
		run(fixture.Exec("EXECUTE domain_default_after_set(10)", 1))
		fixture.RunStep(t, p.Ctx, insert, r, fixture.Exec(insert.SQL, 1, 2))
		run(fixture.Exec("EXECUTE domain_default_insert(3)", 1))
		run(fixture.Exec("ALTER DOMAIN domain_s.p DROP DEFAULT", 0))
		metadata(nil)
		run(fixture.Exec("INSERT INTO domain_s.t(id) VALUES(8)", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO domain_s.t(id) VALUES(9)", 1)))
		run(fixture.Exec("PREPARE domain_default_after_drop(integer) AS INSERT INTO domain_s.t(id) VALUES($1)", 0))
		defer c.ExecContext(p.Ctx, "DEALLOCATE domain_default_after_drop")
		run(fixture.Exec("EXECUTE domain_default_after_drop(11)", 1))
		fixture.RunStep(t, p.Ctx, insert, r, fixture.Exec(insert.SQL, 1, 4))
		run(fixture.Exec("EXECUTE domain_default_insert(5)", 1))
		// PostgreSQL retains rewritten omitted-default recipes. Native retains
		// analysis identities, not rewritten queries or executable plans, and
		// resolves the current recipe. Assert each boundary independently.
		var heldSet any = int64(11)
		var heldDrop any
		if b.PG {
			heldSet, heldDrop = int64(7), int64(7)
		}
		data.Rows = [][]any{{int64(1), int64(7), int64(7)}, {int64(2), heldSet, int64(7)}, {int64(3), heldSet, int64(7)}, {int64(4), heldDrop, int64(7)}, {int64(5), heldDrop, int64(7)}, {int64(6), int64(11), int64(7)}, {int64(7), int64(11), int64(7)}, {int64(8), nil, int64(7)}, {int64(9), nil, int64(7)}, {int64(10), int64(11), int64(7)}, {int64(11), nil, int64(7)}}
		fixture.RunStep(t, p.Ctx, selectHeld, r, data)
	})
}
