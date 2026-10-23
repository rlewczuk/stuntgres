package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Each case isolates a target-label or SubLink branch in released ruleutils.c.
// Full literals are independent candidates pending the pinned PostgreSQL run.
func TestReferencePGRulesExpressionDefinitions(t *testing.T) {
	cases := []struct {
		name, ddl, definition string
		renames               []string
		renamedDefinition     string
	}{
		{
			name:       "unlabeled_computed_select",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT NEW.v+1",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT (new.v + 1) AS \"?column?\";",
		},
		{
			name:       "unlabeled_computed_returning",
			ddl:        "CREATE RULE r AS ON UPDATE TO pgr_src DO INSTEAD UPDATE pgr_log SET v=NEW.v RETURNING id,v+1",
			definition: "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO INSTEAD  UPDATE pgr_log SET v = new.v\n  RETURNING pgr_log.id,\n    (pgr_log.v + 1) AS \"?column?\";",
		},
		{
			name:       "correlated_scalar_current_names",
			ddl:        "CREATE RULE r AS ON UPDATE TO pgr_src DO UPDATE pgr_log AS l SET v=(SELECT q.v FROM pgr_lookup q WHERE q.id=OLD.id) WHERE l.id=OLD.id",
			definition: "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO  UPDATE pgr_log l SET v = ( SELECT q.v\n           FROM pgr_lookup q\n          WHERE (q.id = old.id))\n  WHERE (l.id = old.id);",
			renames: []string{
				"ALTER TABLE pgr_lookup RENAME TO pgr_lookup_now",
				"ALTER TABLE pgr_lookup_now RENAME COLUMN id TO lookup_id",
				"ALTER TABLE pgr_lookup_now RENAME COLUMN v TO lookup_v",
				"ALTER TABLE pgr_src RENAME TO pgr_src_now",
				"ALTER TABLE pgr_src_now RENAME COLUMN id TO source_id",
			},
			renamedDefinition: "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src_now DO  UPDATE pgr_log l SET v = ( SELECT q.lookup_v AS v\n           FROM pgr_lookup_now q\n          WHERE (q.lookup_id = old.source_id))\n  WHERE (l.id = old.source_id);",
		},
		{
			name:       "correlated_exists",
			ddl:        "CREATE RULE r AS ON DELETE TO pgr_src DO DELETE FROM pgr_log AS l WHERE EXISTS(SELECT 1 FROM pgr_lookup q WHERE q.id=l.id)",
			definition: "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO  DELETE FROM pgr_log l\n  WHERE (EXISTS ( SELECT 1\n           FROM pgr_lookup q\n          WHERE (q.id = l.id)));",
		},
		{
			name:       "equality_any_canonical_in",
			ddl:        "CREATE RULE r AS ON UPDATE TO pgr_src DO UPDATE pgr_log SET v=NEW.v WHERE id = ANY(SELECT id FROM pgr_lookup)",
			definition: "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO  UPDATE pgr_log SET v = new.v\n  WHERE (pgr_log.id IN ( SELECT pgr_lookup.id\n           FROM pgr_lookup));",
		},
		{
			name:       "non_equality_any",
			ddl:        "CREATE RULE r AS ON UPDATE TO pgr_src DO UPDATE pgr_log SET v=NEW.v WHERE id < ANY(SELECT id FROM pgr_lookup)",
			definition: "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO  UPDATE pgr_log SET v = new.v\n  WHERE (pgr_log.id < ANY ( SELECT pgr_lookup.id\n           FROM pgr_lookup));",
		},
		{
			name:       "all_sublink",
			ddl:        "CREATE RULE r AS ON UPDATE TO pgr_src DO UPDATE pgr_log SET v=NEW.v WHERE id > ALL(SELECT id FROM pgr_lookup)",
			definition: "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO  UPDATE pgr_log SET v = new.v\n  WHERE (pgr_log.id > ALL ( SELECT pgr_lookup.id\n           FROM pgr_lookup));",
		},
		{
			name:       "row_in_sublink",
			ddl:        "CREATE RULE r AS ON DELETE TO pgr_src DO DELETE FROM pgr_log WHERE (id,v) IN (SELECT id,v FROM pgr_lookup)",
			definition: "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO  DELETE FROM pgr_log\n  WHERE ((pgr_log.id, pgr_log.v) IN ( SELECT pgr_lookup.id,\n            pgr_lookup.v\n           FROM pgr_lookup));",
		},
		{
			name:       "row_comparison_sublink",
			ddl:        "CREATE RULE r AS ON DELETE TO pgr_src DO DELETE FROM pgr_log WHERE (id,v) < (SELECT id,v FROM pgr_lookup)",
			definition: "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO  DELETE FROM pgr_log\n  WHERE ((pgr_log.id, pgr_log.v) < ( SELECT pgr_lookup.id,\n            pgr_lookup.v\n           FROM pgr_lookup));",
		},
		{
			name:       "select_equality_any_canonical_in",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT NEW.id = ANY(SELECT id FROM pgr_lookup)",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT (new.id IN ( SELECT pgr_lookup.id\n           FROM pgr_lookup)) AS \"?column?\";",
		},
		{
			name:       "select_correlated_exists",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT EXISTS(SELECT 1 FROM pgr_lookup q WHERE q.id=NEW.id) AS present",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT (EXISTS ( SELECT 1\n           FROM pgr_lookup q\n          WHERE (q.id = new.id))) AS present;",
		},
		{
			name:       "select_row_comparison_sublink",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT (NEW.id,NEW.v) < (SELECT id,v FROM pgr_lookup)",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT ((new.id, new.v) < ( SELECT pgr_lookup.id,\n            pgr_lookup.v\n           FROM pgr_lookup)) AS \"?column?\";",
		},
		{
			name:       "insert_select_equality_any_composition",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO INSERT INTO pgr_log(id,v) SELECT NEW.id,NEW.v WHERE NEW.id = ANY(SELECT id FROM pgr_lookup)",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log (id, v)  SELECT new.id,\n            new.v\n          WHERE (new.id IN ( SELECT pgr_lookup.id\n                   FROM pgr_lookup));",
		},
		{
			name:       "select_exists_derived_visible_labels",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT EXISTS(SELECT 1 FROM (SELECT 2) AS q) AS present",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT (EXISTS ( SELECT 1\n           FROM ( SELECT 2 AS \"?column?\") q)) AS present;",
		},
		{
			name:       "select_exists_cte_visible_labels",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO SELECT EXISTS(WITH q AS (SELECT 2) SELECT 1 FROM q) AS present",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  SELECT (EXISTS ( WITH q AS (\n                 SELECT 2 AS \"?column?\"\n                )\n         SELECT 1\n           FROM q)) AS present;",
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
						"CREATE TABLE pgr_log(id INT,v INT)",
						"CREATE TABLE pgr_lookup(id INT,v INT)",
						"INSERT INTO pgr_log VALUES(7,70)",
						"INSERT INTO pgr_lookup VALUES(7,700)",
					} {
						count := int64(0)
						if q == "INSERT INTO pgr_log VALUES(7,70)" || q == "INSERT INTO pgr_lookup VALUES(7,700)" {
							count = 1
						}
						run(fixture.Exec(q, count))
					}
					run(fixture.Exec(c.ddl, 0))
					pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.definition}}, "public", "pgr_src", "r")
					for _, q := range c.renames {
						run(fixture.Exec(q, 0))
					}
					source := "pgr_src"
					if len(c.renames) != 0 {
						source = "pgr_src_now"
						pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_src_now", "r", c.renamedDefinition}}, "public", "pgr_src_now", "r")
					}
					// Seeded target rows make accidental UPDATE/DELETE observable;
					// metadata capture must never evaluate the stored subqueries.
					run(fixture.Select("SELECT id,v FROM pgr_log", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(70)}}))
					run(fixture.Select("SELECT count(*) FROM "+source, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					if len(c.renames) != 0 {
						run(fixture.Select("SELECT lookup_id,lookup_v FROM pgr_lookup_now", []string{"lookup_id", "lookup_v"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(700)}}))
					} else {
						run(fixture.Select("SELECT id,v FROM pgr_lookup", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(700)}}))
					}
				})
			}
		})
	}
}
