package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterConstraintLifecycle(t *testing.T) {
	domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE DOMAIN domain_s.d AS integer CONSTRAINT upper_bound CHECK(VALUE<10)", 0))
		run(fixture.Exec("CREATE TABLE domain_s.t(id integer,v domain_s.d)", 0))
		run(fixture.Exec("INSERT INTO domain_s.t VALUES(1,-1),(2,NULL)", 2))
		domain := domainAlterOID(t, p, c, "SELECT 'domain_s.d'::regtype::oid")
		array := domainAlterOID(t, p, c, "SELECT 'domain_s.d[]'::regtype::oid")
		q := "ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0)"
		t.Run("failed_add", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, domainAlterOldFailure(q, "t", "v")) })
		run(domainAlterFlags("d", [][]any{{"upper_bound", true}}))
		run(fixture.Exec(q+" NOT VALID", 0))
		identity := domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid AND conname='positive'")
		run(domainAlterFlags("d", [][]any{{"positive", false}, {"upper_bound", true}}))
		data := fixture.Select("SELECT id,v FROM domain_s.t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(-1)}, {int64(2), nil}})
		data.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, b.PG, false, data.Types) }
		run(data)
		stmt, err := c.PrepareContext(p.Ctx, "INSERT INTO domain_s.t VALUES($1,$2)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: "INSERT INTO domain_s.t VALUES($1,$2)"}
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
				s := domainCheckFailure("INSERT INTO domain_s.t VALUES($1,$2)", "d", "positive", false, false, 3, -2)
				s.Diagnostic.Context = "unnamed portal parameter $2 = '...'"
				var x fixture.Executor = c
				if prepared {
					x = held
				}
				fixture.RunStep(t, p.Ctx, x, r, s)
			})
		}
		fixture.RunStep(t, p.Ctx, held, r, fixture.Exec(held.SQL, 1, 3, 2))
		validate := "ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive"
		t.Run("failed_validate", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, domainAlterOldFailure(validate, "t", "v")) })
		run(domainAlterFlags("d", [][]any{{"positive", false}, {"upper_bound", true}}))
		data.Rows = [][]any{{int64(1), int64(-1)}, {int64(2), nil}, {int64(3), int64(2)}}
		run(data)
		run(fixture.Exec("UPDATE domain_s.t SET v=1 WHERE id=1", 1))
		run(fixture.Prepared(fixture.Exec(validate, 0)))
		run(domainAlterFlags("d", [][]any{{"positive", true}, {"upper_bound", true}}))
		if got := domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid AND conname='positive'"); got != identity {
			t.Fatalf("CHECK identity %d -> %d", identity, got)
		}
		run(fixture.Exec("ALTER DOMAIN domain_s.d DROP CONSTRAINT positive RESTRICT", 0))
		run(domainAlterFlags("d", [][]any{{"upper_bound", true}}))
		fixture.RunStep(t, p.Ctx, held, r, fixture.Exec(held.SQL, 1, 4, -2))
		t.Run("unrelated_check", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure("INSERT INTO domain_s.t VALUES(5,11)", "d", "upper_bound", false, false))
		})
		if got := domainAlterOID(t, p, c, "SELECT 'domain_s.d'::regtype::oid"); got != domain {
			t.Fatalf("domain identity %d -> %d", domain, got)
		}
		if got := domainAlterOID(t, p, c, "SELECT 'domain_s.d[]'::regtype::oid"); got != array {
			t.Fatalf("array identity %d -> %d", array, got)
		}
	})
}

func TestReferenceDomainAlterConstraintTargetOnly(t *testing.T) {
	domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.parent AS integer",
			"CREATE DOMAIN domain_s.child AS domain_s.parent CONSTRAINT child_upper CHECK(VALUE<10)",
			"CREATE TABLE domain_s.t(v domain_s.child)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.t VALUES(-1)", 1))
		run(fixture.Exec("ALTER DOMAIN domain_s.parent ADD CONSTRAINT a_positive CHECK(VALUE>0) NOT VALID", 0))
		run(fixture.Exec("ALTER DOMAIN domain_s.parent ADD CONSTRAINT b_upper CHECK(VALUE<10)", 0))
		run(fixture.Exec("ALTER DOMAIN domain_s.parent VALIDATE CONSTRAINT b_upper", 0))
		run(domainAlterFlags("parent", [][]any{{"a_positive", false}, {"b_upper", true}}))
		for _, action := range []string{"DROP", "VALIDATE"} {
			want := fixture.ControlsReferenceDiagnostic("42704", `constraint "a_positive" of domain "domain_s.child" does not exist`)
			t.Run(action, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("ALTER DOMAIN domain_s.child "+action+" CONSTRAINT a_positive", want))
			})
		}
		run(fixture.Exec("ALTER DOMAIN domain_s.parent DROP CONSTRAINT a_positive CASCADE", 0))
		run(fixture.Select("SELECT (-2)::domain_s.child AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{int64(-2)}}))
		t.Run("child_survives", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure("SELECT 11::domain_s.child FROM public.one", "child", "b_upper", true, false))
		})
		run(fixture.Exec("ALTER DOMAIN domain_s.parent DROP CONSTRAINT b_upper", 0))
		t.Run("own_child_survives", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure("SELECT 11::domain_s.child FROM public.one", "child", "child_upper", true, false))
		})
		// Own alphabetical order still wins over declaration order after ALTER.
		run(fixture.Exec("ALTER DOMAIN domain_s.child ADD CONSTRAINT a_false CHECK(FALSE) NOT VALID", 0))
		t.Run("alphabetical", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure("SELECT 11::domain_s.child FROM public.one", "child", "a_false", true, false))
		})
	})
}

func TestReferenceDomainAlterConstraintRepeatedValidate(t *testing.T) {
	domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE DOMAIN domain_s.d AS integer CONSTRAINT positive CHECK(VALUE>0)", 0))
		run(fixture.Exec("CREATE TABLE domain_s.t(v domain_s.d)", 0))
		run(fixture.Exec("INSERT INTO domain_s.t VALUES(1)", 1))
		id := domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid")
		q := "ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive"
		run(fixture.Exec(q, 0))
		run(fixture.Exec("CREATE TABLE domain_s.holder(x domain_s.d[])", 0))
		t.Run("release18_rescans", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, domainAlterContainerFailure(q, "d", "holder", "x"))
		})
		run(domainAlterFlags("d", [][]any{{"positive", true}}))
		if got := domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid"); got != id {
			t.Fatalf("CHECK identity %d -> %d", id, got)
		}
	})
}
