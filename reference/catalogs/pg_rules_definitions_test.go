package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGRulesDefinitions(t *testing.T) {
	cases := []struct {
		name, ddl, definition string
	}{
		{"default", "CREATE RULE r AS ON INSERT TO pgr_src DO NOTHING", "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO NOTHING;"},
		{"explicit_also", "CREATE RULE r AS ON INSERT TO pgr_src DO ALSO NOTHING", "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO NOTHING;"},
		{"canonical_source", "create rule \"r\" as on insert to \"public\" . \"pgr_src\" do also nothing", "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO NOTHING;"},
		{"instead", "CREATE RULE r AS ON DELETE TO pgr_src DO INSTEAD NOTHING", "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO INSTEAD NOTHING;"},
		{"conditional", "CREATE RULE r AS ON UPDATE TO pgr_src WHERE NEW.id>0 DO INSTEAD NOTHING", "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src\n   WHERE (new.id > 0) DO INSTEAD NOTHING;"},
		{"insert_typed", "CREATE RULE r AS ON INSERT TO pgr_src DO INSERT INTO pgr_log VALUES(NEW.id,'text',1.25,NULL)", "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log (id, label, amount, optional)\n  VALUES (new.id, 'text'::text, 1.25, NULL::integer);"},
		{"update", "CREATE RULE r AS ON UPDATE TO pgr_src DO UPDATE pgr_log SET id=NEW.id WHERE id=OLD.id", "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO  UPDATE pgr_log SET id = new.id\n  WHERE (pgr_log.id = old.id);"},
		{"delete", "CREATE RULE r AS ON DELETE TO pgr_src DO DELETE FROM pgr_log WHERE id=OLD.id", "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO  DELETE FROM pgr_log\n  WHERE (pgr_log.id = old.id);"},
		{"select", "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT NEW.id", "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT new.id;"},
		{"select_alias", "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT NEW.id AS copied_id", "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT new.id AS copied_id;"},
		{"ordered_actions", "CREATE RULE r AS ON INSERT TO pgr_src DO (INSERT INTO pgr_log(id) VALUES(NEW.id); SELECT NEW.id)", "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO ( INSERT INTO pgr_log (id)\n  VALUES (new.id);\n SELECT new.id;\n);"},
		{"returning", "CREATE RULE r AS ON INSERT TO pgr_src DO INSTEAD INSERT INTO pgr_log(id) VALUES(NEW.id) RETURNING id", "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO INSTEAD  INSERT INTO pgr_log (id)\n  VALUES (new.id)\n  RETURNING pgr_log.id;"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					for _, q := range []string{"CREATE TABLE pgr_src(id INT)", "CREATE TABLE pgr_log(id INT,label TEXT,amount NUMERIC,optional INT)", c.ddl} {
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
					}
					pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.definition}}, "public", "pgr_src", "r")
					for _, table := range []string{"pgr_src", "pgr_log"} {
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT count(*) FROM "+table, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
				})
			}
		})
	}
	t.Run("presentation", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "pgr_actions")
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				run := func(q string) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0)) }
				for _, q := range []string{"CREATE SCHEMA pgr_actions", `CREATE TABLE public."Source Table"("Mixed ID" INT)`, `CREATE TABLE pgr_actions."Log Table"("Mixed ID" INT)`, `CREATE RULE "Mixed Rule" AS ON INSERT TO public."Source Table" DO INSERT INTO pgr_actions."Log Table" VALUES(NEW."Mixed ID")`} {
					run(q)
				}
				check := func(def string) {
					pgRulesCheck(t, p, b, conn, pgRulesProbe, [][]any{{"public", "Source Table", "Mixed Rule", def}}, "public", "Source Table", "Mixed Rule")
				}
				check("CREATE RULE \"Mixed Rule\" AS\n    ON INSERT TO public.\"Source Table\" DO  INSERT INTO pgr_actions.\"Log Table\" (\"Mixed ID\")\n  VALUES (new.\"Mixed ID\");")
				run("SET search_path TO pgr_actions,public")
				check("CREATE RULE \"Mixed Rule\" AS\n    ON INSERT TO public.\"Source Table\" DO  INSERT INTO \"Log Table\" (\"Mixed ID\")\n  VALUES (new.\"Mixed ID\");")
				run("SET quote_all_identifiers TO on")
				check("CREATE RULE \"Mixed Rule\" AS\n    ON INSERT TO \"public\".\"Source Table\" DO  INSERT INTO \"Log Table\" (\"Mixed ID\")\n  VALUES (\"new\".\"Mixed ID\");")
				run("RESET quote_all_identifiers")
				run("RESET search_path")
			})
		}
	})
	t.Run("nonstandard_strings", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				for _, q := range []string{"CREATE TABLE pgr_src(id INT)", "CREATE TABLE pgr_log(label TEXT)", `CREATE RULE r AS ON INSERT TO pgr_src DO INSERT INTO pgr_log VALUES(E'a\\\\b')`, "SET standard_conforming_strings TO off"} {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0))
				}
				pgRulesCheck(t, p, b, conn, pgRulesProbe, [][]any{{"public", "pgr_src", "r", "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log (label)\n  VALUES ('a\\\\\\\\b'::text);"}}, "public", "pgr_src", "r")
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET standard_conforming_strings", 0))
			})
		}
	})
}
