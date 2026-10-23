package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Candidate literals follow REL_18_6 ruleutils.c's distinct mutation branches;
// the pinned oracle must resolve them before any renderer changes.
func TestReferencePGRulesMutationForms(t *testing.T) {
	cases := []struct {
		name, ddl, definition string
		renames               []string
		renamedDefinition     string
	}{
		{
			name:       "update_only_current_target",
			ddl:        "CREATE RULE r AS ON UPDATE TO pgr_src DO UPDATE ONLY pgr_log AS l SET v=NEW.v WHERE l.id=OLD.id",
			definition: "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO  UPDATE ONLY pgr_log l SET v = new.v\n  WHERE (l.id = old.id);",
			renames: []string{
				"ALTER TABLE pgr_log RENAME TO pgr_log_now",
				"ALTER TABLE pgr_log_now RENAME COLUMN id TO log_id",
				"ALTER TABLE pgr_log_now RENAME COLUMN v TO log_v",
				"ALTER TABLE pgr_src RENAME COLUMN id TO source_id",
				"ALTER TABLE pgr_src RENAME COLUMN v TO source_v",
			},
			renamedDefinition: "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO  UPDATE ONLY pgr_log_now l SET log_v = new.source_v\n  WHERE (l.log_id = old.source_id);",
		},
		{
			name:       "delete_only_current_target",
			ddl:        "CREATE RULE r AS ON DELETE TO pgr_src DO DELETE FROM ONLY pgr_log AS l WHERE l.id=OLD.id",
			definition: "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO  DELETE FROM ONLY pgr_log l\n  WHERE (l.id = old.id);",
			renames: []string{
				"ALTER TABLE pgr_log RENAME TO pgr_log_now",
				"ALTER TABLE pgr_log_now RENAME COLUMN id TO log_id",
				"ALTER TABLE pgr_src RENAME COLUMN id TO source_id",
			},
			renamedDefinition: "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO  DELETE FROM ONLY pgr_log_now l\n  WHERE (l.log_id = old.source_id);",
		},
		{
			name:       "default_values_inline",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO INSERT INTO pgr_log DEFAULT VALUES",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log DEFAULT VALUES;",
		},
		{
			name:       "multirow_values_current_attributes",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO INSERT INTO pgr_log VALUES(NEW.id,NEW.v),(NEW.id+1,NEW.v+1)",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log (id, v) VALUES (new.id,new.v), ((new.id + 1),(new.v + 1));",
			renames: []string{
				"ALTER TABLE pgr_log RENAME COLUMN id TO log_id",
				"ALTER TABLE pgr_log RENAME COLUMN v TO log_v",
				"ALTER TABLE pgr_src RENAME COLUMN id TO source_id",
				"ALTER TABLE pgr_src RENAME COLUMN v TO source_v",
			},
			renamedDefinition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log (log_id, log_v) VALUES (new.source_id,new.source_v), ((new.source_id + 1),(new.source_v + 1));",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, q := range []string{
						"CREATE TABLE pgr_src(id INT,v INT)",
						"CREATE TABLE pgr_log(id INT DEFAULT 7,v INT DEFAULT 70)",
						c.ddl,
					} {
						run(fixture.Exec(q, 0))
					}
					pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.definition}}, "public", "pgr_src", "r")
					for _, q := range c.renames {
						run(fixture.Exec(q, 0))
					}
					if len(c.renames) != 0 {
						pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.renamedDefinition}}, "public", "pgr_src", "r")
					}
					// Defaults and both VALUES rows are stored recipes, not work
					// performed by either route through pg_rules.
					run(fixture.Select("SELECT count(*) FROM pgr_src", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					log := "pgr_log"
					if c.name == "update_only_current_target" || c.name == "delete_only_current_target" {
						log = "pgr_log_now"
					}
					run(fixture.Select("SELECT count(*) FROM "+log, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
			}
		})
	}
}
