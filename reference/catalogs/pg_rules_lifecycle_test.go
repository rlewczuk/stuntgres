package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGRulesLifecycle(t *testing.T) {
	t.Run("publication", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE pgr_src(id INT)", 0))
				run(fixture.Exec("CREATE TABLE pgr_sibling(id INT)", 0))
				held := fixture.ViewHold(t, p, b, pgRulesProbe)
				check := func(name, def string) {
					var want [][]any
					if def != "" {
						want = [][]any{{"public", "pgr_src", name, def}}
					}
					pgRulesCheck(t, p, b, b.DB, pgRulesProbe, want, "public", "pgr_src", name)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTableViewSelect(b, pgRulesProbe, pgRulesColumns, pgRulesTypes, want, "public", "pgr_src", name))
				}
				check("r", "")
				run(fixture.Exec("CREATE RULE r AS ON INSERT TO pgr_src DO INSTEAD NOTHING", 0))
				run(fixture.Exec("CREATE RULE r AS ON INSERT TO pgr_sibling DO NOTHING", 0))
				initial := "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO INSTEAD NOTHING;"
				check("r", initial)
				var consumeOld func()
				if !b.PG {
					old, err := b.DB.QueryContext(p.Ctx, pgRulesProbe, "public", "pgr_src", "r")
					if err != nil {
						t.Fatal(err)
					}
					defer old.Close()
					types, err := old.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					consumeOld = func() { pgRulesRetained(t, old, types, []any{"public", "pgr_src", "r", initial}) }
				}
				for _, c := range []struct{ q, message string }{
					{"CREATE RULE r AS ON INSERT TO pgr_src DO NOTHING", `rule "r" for relation "pgr_src" already exists`},
					{"ALTER RULE r ON pgr_src RENAME TO z", `rule "z" for relation "pgr_src" already exists`},
				} {
					if c.q == "ALTER RULE r ON pgr_src RENAME TO z" {
						run(fixture.Exec("CREATE RULE z AS ON DELETE TO pgr_src DO NOTHING", 0))
					}
					run(fixture.ImplicitError(c.q, fixture.Diagnostic{Code: "42710", Message: c.message}))
					check("r", initial)
				}
				run(fixture.Exec("CREATE OR REPLACE RULE r AS ON INSERT TO pgr_src DO NOTHING", 0))
				check("r", "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO NOTHING;")
				run(fixture.Exec("ALTER RULE r ON pgr_src RENAME TO renamed", 0))
				check("r", "")
				check("renamed", "CREATE RULE renamed AS\n    ON INSERT TO public.pgr_src DO NOTHING;")
				for _, q := range []string{"ALTER RULE missing ON pgr_src RENAME TO absent", "DROP RULE missing ON pgr_src"} {
					run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42704", Message: `rule "missing" for relation "pgr_src" does not exist`}))
					check("renamed", "CREATE RULE renamed AS\n    ON INSERT TO public.pgr_src DO NOTHING;")
				}
				run(fixture.Exec("DROP RULE renamed ON pgr_src", 0))
				check("renamed", "")
				pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_sibling", "r", "CREATE RULE r AS\n    ON INSERT TO public.pgr_sibling DO NOTHING;"}}, "public", "pgr_sibling", "r")
				if consumeOld != nil {
					consumeOld()
				}
				var notices []fixture.Diagnostic
				_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DROP RULE IF EXISTS missing ON pgr_src", 0))
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `rule "missing" for relation "pgr_src" does not exist, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("notices: %#v, want %#v", notices, want)
				}
				check("renamed", "")
			})
		}
	})
	t.Run("current_identities", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "pgr_before", "pgr_after")
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				run := func(q string) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0)) }
				for _, q := range []string{"CREATE SCHEMA pgr_before", "CREATE TABLE pgr_before.src(id INT)", "CREATE TABLE pgr_before.log(id INT)", "CREATE RULE r AS ON INSERT TO pgr_before.src DO INSERT INTO pgr_before.log VALUES(NEW.id)"} {
					run(q)
				}
				pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"pgr_before", "src", "r", "CREATE RULE r AS\n    ON INSERT TO pgr_before.src DO  INSERT INTO pgr_before.log (id)\n  VALUES (new.id);"}}, "pgr_before", "src", "r")
				for _, q := range []string{"ALTER TABLE pgr_before.src RENAME TO source_now", "ALTER TABLE pgr_before.log RENAME TO log_now", "ALTER TABLE pgr_before.source_now RENAME COLUMN id TO source_id", "ALTER TABLE pgr_before.log_now RENAME COLUMN id TO log_id", "ALTER SCHEMA pgr_before RENAME TO pgr_after", "CREATE SCHEMA pgr_before", "CREATE TABLE pgr_before.src(id INT)", "CREATE TABLE pgr_before.log(id INT)"} {
					run(q)
				}
				pgRulesCheck(t, p, b, b.DB, pgRulesProbe, nil, "pgr_before", "src", "r")
				pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"pgr_after", "source_now", "r", "CREATE RULE r AS\n    ON INSERT TO pgr_after.source_now DO  INSERT INTO pgr_after.log_now (log_id)\n  VALUES (new.source_id);"}}, "pgr_after", "source_now", "r")
			})
		}
	})
}
