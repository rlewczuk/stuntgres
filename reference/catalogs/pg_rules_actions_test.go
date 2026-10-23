package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// These literals specify catalog display independently of replay SQL. Each
// source shape owns a small scenario so failures identify a rendering contract.
func TestReferencePGRulesCompoundDefinitions(t *testing.T) {
	cases := []struct {
		name, ddl, definition string
		renames               []string
		renamedDefinition     string
	}{
		{
			name:       "multi_target_select",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT NEW.id,NEW.v",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT new.id,\n    new.v;",
		},
		{
			name:       "insert_select",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO INSERT INTO pgr_log(id,v) SELECT NEW.id,NEW.v",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log (id, v)  SELECT new.id,\n            new.v;",
		},
		{
			name:              "update_from_current_names",
			ddl:               "CREATE RULE r AS ON UPDATE TO pgr_src DO UPDATE pgr_log SET v=NEW.v FROM pgr_lookup WHERE pgr_log.id=pgr_lookup.id AND pgr_lookup.id=OLD.id",
			definition:        "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO  UPDATE pgr_log SET v = new.v\n   FROM pgr_lookup\n  WHERE ((pgr_log.id = pgr_lookup.id) AND (pgr_lookup.id = old.id));",
			renames:           []string{"ALTER TABLE pgr_lookup RENAME TO pgr_lookup_now", "ALTER TABLE pgr_lookup_now RENAME COLUMN id TO lookup_id", "ALTER TABLE pgr_log RENAME COLUMN id TO log_id", "ALTER TABLE pgr_src RENAME COLUMN id TO source_id"},
			renamedDefinition: "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO  UPDATE pgr_log SET v = new.v\n   FROM pgr_lookup_now\n  WHERE ((pgr_log.log_id = pgr_lookup_now.lookup_id) AND (pgr_lookup_now.lookup_id = old.source_id));",
		},
		{
			name:              "update_from_joined_derived_current_names",
			ddl:               "CREATE RULE r AS ON UPDATE TO pgr_src DO UPDATE pgr_log SET v=NEW.v+q.v FROM pgr_lookup a JOIN (SELECT id,v FROM pgr_other) q ON a.id=q.id WHERE pgr_log.id=a.id AND a.id=OLD.id",
			definition:        "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO  UPDATE pgr_log SET v = (new.v + q.v)\n   FROM (pgr_lookup a\n     JOIN ( SELECT pgr_other.id,\n            pgr_other.v\n           FROM pgr_other) q ON ((a.id = q.id)))\n  WHERE ((pgr_log.id = a.id) AND (a.id = old.id));",
			renames:           []string{"ALTER TABLE pgr_other RENAME TO pgr_other_now", "ALTER TABLE pgr_other_now RENAME COLUMN id TO other_id", "ALTER TABLE pgr_lookup RENAME COLUMN id TO lookup_id", "ALTER TABLE pgr_src RENAME COLUMN id TO source_id"},
			renamedDefinition: "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO  UPDATE pgr_log SET v = (new.v + q.v)\n   FROM (pgr_lookup a\n     JOIN ( SELECT pgr_other_now.other_id AS id,\n            pgr_other_now.v\n           FROM pgr_other_now) q ON ((a.lookup_id = q.id)))\n  WHERE ((pgr_log.id = a.lookup_id) AND (a.lookup_id = old.source_id));",
		},
		{
			name:       "delete_using_alias",
			ddl:        "CREATE RULE r AS ON DELETE TO pgr_src DO DELETE FROM pgr_log USING pgr_lookup AS q WHERE pgr_log.id=q.id AND q.id=OLD.id",
			definition: "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO  DELETE FROM pgr_log\n   USING pgr_lookup q\n  WHERE ((pgr_log.id = q.id) AND (q.id = old.id));",
		},
		{
			name:       "delete_using_derived",
			ddl:        "CREATE RULE r AS ON DELETE TO pgr_src DO DELETE FROM pgr_log USING (SELECT id FROM pgr_lookup) AS q WHERE pgr_log.id=q.id AND q.id=OLD.id",
			definition: "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO  DELETE FROM pgr_log\n   USING ( SELECT pgr_lookup.id\n           FROM pgr_lookup) q\n  WHERE ((pgr_log.id = q.id) AND (q.id = old.id));",
		},
		{
			name:              "delete_using_joined_derived_current_names",
			ddl:               "CREATE RULE r AS ON DELETE TO pgr_src DO DELETE FROM pgr_log USING pgr_lookup a JOIN (SELECT id FROM pgr_other) q ON a.id=q.id WHERE pgr_log.id=q.id AND q.id=OLD.id",
			definition:        "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO  DELETE FROM pgr_log\n   USING (pgr_lookup a\n     JOIN ( SELECT pgr_other.id\n           FROM pgr_other) q ON ((a.id = q.id)))\n  WHERE ((pgr_log.id = q.id) AND (q.id = old.id));",
			renames:           []string{"ALTER TABLE pgr_other RENAME TO pgr_other_now", "ALTER TABLE pgr_other_now RENAME COLUMN id TO other_id", "ALTER TABLE pgr_lookup RENAME COLUMN id TO lookup_id", "ALTER TABLE pgr_src RENAME COLUMN id TO source_id"},
			renamedDefinition: "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO  DELETE FROM pgr_log\n   USING (pgr_lookup a\n     JOIN ( SELECT pgr_other_now.other_id AS id\n           FROM pgr_other_now) q ON ((a.lookup_id = q.id)))\n  WHERE ((pgr_log.id = q.id) AND (q.id = old.source_id));",
		},
		{
			name:              "joined_select_current_names",
			ddl:               "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT a.id,b.v FROM pgr_lookup AS a JOIN pgr_other AS b ON a.id=b.id WHERE a.id=NEW.id",
			definition:        "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT a.id,\n    b.v\n   FROM (pgr_lookup a\n     JOIN pgr_other b ON ((a.id = b.id)))\n  WHERE (a.id = new.id);",
			renames:           []string{"ALTER TABLE pgr_lookup RENAME TO pgr_lookup_now", "ALTER TABLE pgr_lookup_now RENAME COLUMN id TO lookup_id", "ALTER TABLE pgr_other RENAME COLUMN v TO other_value", "ALTER TABLE pgr_src RENAME COLUMN id TO source_id"},
			renamedDefinition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT a.lookup_id AS id,\n    b.other_value AS v\n   FROM (pgr_lookup_now a\n     JOIN pgr_other b ON ((a.lookup_id = b.id)))\n  WHERE (a.lookup_id = new.source_id);",
		},
		{
			name:       "derived_select",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT q.id,q.v FROM (SELECT id,v FROM pgr_lookup) AS q WHERE q.id=NEW.id",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT q.id,\n    q.v\n   FROM ( SELECT pgr_lookup.id,\n            pgr_lookup.v\n           FROM pgr_lookup) q\n  WHERE (q.id = new.id);",
		},
		{
			name:       "returning_alias_and_computed",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO INSTEAD INSERT INTO pgr_log VALUES(NEW.id,NEW.v) RETURNING id AS copied_id,v+1 AS next_v",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO INSTEAD  INSERT INTO pgr_log (id, v)\n  VALUES (new.id, new.v)\n  RETURNING pgr_log.id AS copied_id,\n    (pgr_log.v + 1) AS next_v;",
		},
		{
			name:       "modifier_cte_returning_consumer",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO WITH moved AS (INSERT INTO pgr_aux VALUES(7,70) RETURNING id AS moved_id,v+1 AS moved_v) INSERT INTO pgr_log SELECT NEW.id,moved.moved_v FROM moved",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  WITH moved AS (\n         INSERT INTO pgr_aux (id, v)\n          VALUES (7, 70)\n          RETURNING pgr_aux.id AS moved_id,\n            (pgr_aux.v + 1) AS moved_v\n        )\n INSERT INTO pgr_log (id, v)  SELECT new.id,\n            moved.moved_v\n           FROM moved;",
		},
		{
			name:       "modifier_cte_target_namespace_collision",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO WITH moved AS (INSERT INTO pgr_log VALUES(7,70) RETURNING id,v) INSERT INTO pgr_log SELECT moved.id,moved.v FROM moved",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  WITH moved AS (\n         INSERT INTO pgr_log AS pgr_log_1 (id, v)\n          VALUES (7, 70)\n          RETURNING pgr_log_1.id,\n            pgr_log_1.v\n        )\n INSERT INTO pgr_log (id, v)  SELECT moved.id,\n            moved.v\n           FROM moved;",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, q := range []string{"CREATE TABLE pgr_src(id INT,v INT)", "CREATE TABLE pgr_log(id INT,v INT)", "CREATE TABLE pgr_lookup(id INT,v INT)", "CREATE TABLE pgr_other(id INT,v INT)", "CREATE TABLE pgr_aux(id INT,v INT)", c.ddl} {
						run(fixture.Exec(q, 0))
					}
					pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.definition}}, "public", "pgr_src", "r")
					for _, q := range c.renames {
						run(fixture.Exec(q, 0))
					}
					if len(c.renames) != 0 {
						pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.renamedDefinition}}, "public", "pgr_src", "r")
					}
					// Repeated direct/prepared presentation, including a modifier CTE,
					// must not execute either the event or any action relation.
					for _, table := range []string{"pgr_src", "pgr_log", "pgr_aux"} {
						run(fixture.Select("SELECT count(*) FROM "+table, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
				})
			}
		})
	}
}
