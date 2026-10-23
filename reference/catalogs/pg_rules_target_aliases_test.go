package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGRulesTargetAliases(t *testing.T) {
	cases := []struct {
		name, ddl, definition, renamedDefinition string
	}{
		{
			name:              "update_quoted_target",
			ddl:               `CREATE RULE r AS ON UPDATE TO pgr_src DO INSTEAD UPDATE pgr_log AS "Target Alias" SET v=NEW.v WHERE "Target Alias".id=OLD.id RETURNING "Target Alias".id AS saved_id,"Target Alias".v AS saved_v`,
			definition:        "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO INSTEAD  UPDATE pgr_log \"Target Alias\" SET v = new.v\n  WHERE (\"Target Alias\".id = old.id)\n  RETURNING \"Target Alias\".id AS saved_id,\n    \"Target Alias\".v AS saved_v;",
			renamedDefinition: "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO INSTEAD  UPDATE pgr_log_now \"Target Alias\" SET log_v = new.source_v\n  WHERE (\"Target Alias\".log_id = old.source_id)\n  RETURNING \"Target Alias\".log_id AS saved_id,\n    \"Target Alias\".log_v AS saved_v;",
		},
		{
			name:              "delete_target_source_name_collision",
			ddl:               "CREATE RULE r AS ON DELETE TO pgr_src DO INSTEAD DELETE FROM pgr_log AS l USING pgr_lookup AS pgr_log WHERE l.id=pgr_log.id AND l.id=OLD.id RETURNING l.id AS removed_id,l.v AS removed_v",
			definition:        "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO INSTEAD  DELETE FROM pgr_log l\n   USING pgr_lookup pgr_log\n  WHERE ((l.id = pgr_log.id) AND (l.id = old.id))\n  RETURNING l.id AS removed_id,\n    l.v AS removed_v;",
			renamedDefinition: "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO INSTEAD  DELETE FROM pgr_log_now l\n   USING pgr_lookup pgr_log\n  WHERE ((l.log_id = pgr_log.lookup_id) AND (l.log_id = old.source_id))\n  RETURNING l.log_id AS removed_id,\n    l.log_v AS removed_v;",
		},
		{
			name:              "insert_target_returning",
			ddl:               "CREATE RULE r AS ON INSERT TO pgr_src DO INSTEAD INSERT INTO pgr_log AS l VALUES(NEW.id,NEW.v) RETURNING l.id AS copied_id,l.v AS copied_v",
			definition:        "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO INSTEAD  INSERT INTO pgr_log AS l (id, v)\n  VALUES (new.id, new.v)\n  RETURNING l.id AS copied_id,\n    l.v AS copied_v;",
			renamedDefinition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO INSTEAD  INSERT INTO pgr_log_now AS l (log_id, log_v)\n  VALUES (new.source_id, new.source_v)\n  RETURNING l.log_id AS copied_id,\n    l.log_v AS copied_v;",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, q := range []string{"CREATE TABLE pgr_src(id INT,v INT)", "CREATE TABLE pgr_log(id INT,v INT)", "CREATE TABLE pgr_lookup(id INT,v INT)", c.ddl} {
						run(fixture.Exec(q, 0))
					}
					pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.definition}}, "public", "pgr_src", "r")
					for _, q := range []string{"ALTER TABLE pgr_log RENAME TO pgr_log_now", "ALTER TABLE pgr_log_now RENAME COLUMN id TO log_id", "ALTER TABLE pgr_log_now RENAME COLUMN v TO log_v", "ALTER TABLE pgr_src RENAME COLUMN id TO source_id", "ALTER TABLE pgr_src RENAME COLUMN v TO source_v", "ALTER TABLE pgr_lookup RENAME COLUMN id TO lookup_id"} {
						run(fixture.Exec(q, 0))
					}
					pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.renamedDefinition}}, "public", "pgr_src", "r")
					for _, table := range []string{"pgr_src", "pgr_log_now", "pgr_lookup"} {
						run(fixture.Select("SELECT count(*) FROM "+table, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
				})
			}
		})
	}
}
