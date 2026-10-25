package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalTypesIdentity(t *testing.T) {
	for _, family := range []string{"domain", "composite", "enum", "installed"} {
		t.Run(family, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "tt_dest")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE SCHEMA tt_dest", 0))
					name, decl, literal := "tt_d", "CREATE DOMAIN tt_d AS integer", "7"
					switch family {
					case "composite":
						decl = "CREATE TYPE tt_d AS(id integer,note text)"
						literal = "'(7,x)'"
					case "enum":
						decl = "CREATE TYPE tt_d AS ENUM('x')"
						literal = "'x'"
					case "installed":
						decl = "CREATE EXTENSION citext"
						name = "citext"
						literal = "'MiXeD'"
					}
					run(fixture.Exec(decl, 0))
					run(fixture.Exec("CREATE TABLE tt_holder(v "+name+",keep integer)", 0))
					run(fixture.Exec("INSERT INTO tt_holder VALUES("+literal+",11),(NULL,12)", 2))
					id := domainAlterOID(t, p, c, "SELECT '"+name+"'::regtype::oid")
					array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='"+name+"'::regtype")
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					verb := "TYPE"
					if family == "domain" {
						verb = "DOMAIN"
					}
					run(fixture.Exec("ALTER "+verb+" "+name+" RENAME TO tt_moved", 0))
					run(fixture.Exec("ALTER "+verb+" tt_moved SET SCHEMA tt_dest", 0))
					domainIdentitySameOID(t, p, c, "SELECT 'tt_dest.tt_moved'::regtype::oid", id)
					domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tt_dest.tt_moved'::regtype", array)
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace='tt_dest'::regnamespace AND typname='tt_moved'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Exec("SAVEPOINT moved", 0))
					if family != "installed" {
						d := fixture.ControlsReferenceDiagnostic("42704", `type "tt_missing" does not exist`)
						run(fixture.ErrorDiagnostic("DROP "+verb+" tt_dest.tt_moved,tt_missing CASCADE", d))
						run(fixture.Exec("ROLLBACK TO moved", 0))
						domainIdentitySameOID(t, p, c, "SELECT 'tt_dest.tt_moved'::regtype::oid", id)
						run(fixture.Exec("DROP "+verb+" tt_dest.tt_moved CASCADE", 0))
						run(fixture.Select("SELECT keep FROM tt_holder ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}}))
						run(fixture.Exec("ROLLBACK TO moved", 0))
					}
					run(fixture.Exec("ROLLBACK", 0))
					domainIdentitySameOID(t, p, c, "SELECT '"+name+"'::regtype::oid", id)
				})
			}
		})
	}
	t.Run("zero_column_and_fresh_identity", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE DOMAIN tt_d AS integer", 0))
				run(fixture.Exec("CREATE TABLE tt_zero(v tt_d)", 0))
				run(fixture.Exec("INSERT INTO tt_zero VALUES(7),(NULL)", 2))
				old := domainAlterOID(t, p, c, "SELECT 'tt_d'::regtype::oid")
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("DROP DOMAIN tt_d CASCADE", 0))
				run(fixture.Select("SELECT count(*) FROM tt_zero", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
				run(fixture.Exec("CREATE DOMAIN tt_d AS integer", 0))
				fresh := domainAlterOID(t, p, c, "SELECT 'tt_d'::regtype::oid")
				if fresh == old {
					t.Fatal("drop/recreate reused identity")
				}
				run(fixture.Exec("ROLLBACK", 0))
				domainIdentitySameOID(t, p, c, "SELECT 'tt_d'::regtype::oid", old)
			})
		}
	})
}

func TestReferenceTransactionalTypesSavepoints(t *testing.T) {
	for _, a := range transactionalTypeActions() {
		t.Run(a.name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "tt_dest")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					for _, q := range append([]string{"CREATE SCHEMA tt_dest", "CREATE DOMAIN tt_d AS integer", "CREATE TYPE tt_pair AS(id integer,note text)", "CREATE TABLE tt_typed OF tt_pair", "CREATE TABLE tt_mark_rows(v integer)"}, a.setup...) {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO tt_typed VALUES(7,'x')", 1))
					var before int64
					if err := c.QueryRowContext(p.Ctx, a.probe).Scan(&before); err != nil {
						t.Fatal(err)
					}
					run(fixture.Exec("SAVEPOINT old", 0))
					run(fixture.Exec("CREATE DOMAIN tt_after AS integer", 0))
					run(fixture.Exec(a.query, 0))
					want := int64(1)
					switch a.name {
					case "create_domain", "create_composite", "domain_rename", "domain_move", "type_rename", "type_move":
						want = 2
					case "drop_domain", "drop_type":
						want = 0
					}
					run(fixture.Select(a.probe, []string{"count"}, []string{"INT8"}, [][]any{{want}}))
					run(fixture.Exec("SAVEPOINT newer", 0))
					run(fixture.Exec("INSERT INTO tt_mark_rows VALUES(9)", 1))
					run(fixture.Exec("RELEASE newer", 0))
					run(fixture.Exec("ROLLBACK TO old", 0))
					run(fixture.Exec("ROLLBACK TO old", 0))
					run(fixture.Select(a.probe, []string{"count"}, []string{"INT8"}, [][]any{{before}}))
					run(fixture.Select("SELECT id,note FROM tt_typed", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "x"}}))
					run(fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname='tt_after'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Select("SELECT count(*) FROM tt_mark_rows", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Exec("RELEASE old", 0))
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_d','tt_pair')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
			}
		})
	}
}
