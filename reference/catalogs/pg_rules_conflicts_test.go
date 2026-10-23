package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGRulesConflicts(t *testing.T) {
	cases := []struct {
		name, ddl, definition, renamedDefinition string
		setup, extraRenames                      []string
		quotedDefinition                         string
		offPathDefinition                        string
	}{
		{
			name:              "update_excluded_target_alias",
			ddl:               "CREATE RULE r AS ON INSERT TO pgr_src DO INSERT INTO pgr_log AS l VALUES(NEW.id,NEW.v) ON CONFLICT(id) DO UPDATE SET v=EXCLUDED.v WHERE EXCLUDED.v>l.v",
			definition:        "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log AS l (id, v)\n  VALUES (new.id, new.v) ON CONFLICT(id) DO UPDATE SET v = excluded.v\n  WHERE (excluded.v > l.v);",
			renamedDefinition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log_now AS l (log_id, log_v)\n  VALUES (new.source_id, new.source_v) ON CONFLICT(log_id) DO UPDATE SET log_v = excluded.log_v\n  WHERE (excluded.log_v > l.log_v);",
			quotedDefinition:  "CREATE RULE \"r\" AS\n    ON INSERT TO \"public\".\"pgr_src\" DO  INSERT INTO \"pgr_log\" AS \"l\" (\"id\", \"v\")\n  VALUES (\"new\".\"id\", \"new\".\"v\") ON CONFLICT(\"id\") DO UPDATE SET \"v\" = \"excluded\".\"v\"\n  WHERE (\"excluded\".\"v\" > \"l\".\"v\");",
		},
		{
			name:              "inference_predicate_do_nothing",
			setup:             []string{"CREATE UNIQUE INDEX pgr_partial ON pgr_log(id) WHERE v>0"},
			ddl:               "CREATE RULE r AS ON INSERT TO pgr_src DO INSERT INTO pgr_log AS l VALUES(NEW.id,NEW.v) ON CONFLICT(id) WHERE v>0 DO NOTHING",
			definition:        "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log AS l (id, v)\n  VALUES (new.id, new.v) ON CONFLICT(id)\n  WHERE (v > 0) DO NOTHING;",
			renamedDefinition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log_now AS l (log_id, log_v)\n  VALUES (new.source_id, new.source_v) ON CONFLICT(log_id)\n  WHERE (log_v > 0) DO NOTHING;",
		},
		{
			name:              "named_constraint_current_identity",
			ddl:               "CREATE RULE r AS ON INSERT TO pgr_src DO INSERT INTO pgr_log VALUES(NEW.id,NEW.v) ON CONFLICT ON CONSTRAINT pgr_key DO NOTHING",
			definition:        "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log (id, v)\n  VALUES (new.id, new.v) ON CONFLICT ON CONSTRAINT pgr_key DO NOTHING;",
			extraRenames:      []string{"ALTER TABLE pgr_log_now RENAME CONSTRAINT pgr_key TO pgr_key_now"},
			renamedDefinition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log_now (log_id, log_v)\n  VALUES (new.source_id, new.source_v) ON CONFLICT ON CONSTRAINT pgr_key_now DO NOTHING;",
		},
		{
			name:              "expression_inference_default_opclass",
			ddl:               "CREATE RULE r AS ON INSERT TO pgr_src DO INSERT INTO pgr_log VALUES(NEW.id,NEW.v) ON CONFLICT ((id+1) int4_ops) DO NOTHING",
			definition:        "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log (id, v)\n  VALUES (new.id, new.v) ON CONFLICT(((id + 1))) DO NOTHING;",
			renamedDefinition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log_now (log_id, log_v)\n  VALUES (new.source_id, new.source_v) ON CONFLICT(((log_id + 1))) DO NOTHING;",
		},
		{
			name:              "bare_function_inference",
			ddl:               "CREATE RULE r AS ON INSERT TO pgr_src DO INSERT INTO pgr_log VALUES(NEW.id,NEW.v) ON CONFLICT(abs(id)) DO NOTHING",
			definition:        "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log (id, v)\n  VALUES (new.id, new.v) ON CONFLICT(abs(id)) DO NOTHING;",
			renamedDefinition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log_now (log_id, log_v)\n  VALUES (new.source_id, new.source_v) ON CONFLICT(abs(log_id)) DO NOTHING;",
		},
		{
			name:              "nondefault_class_collation",
			setup:             []string{"ALTER TABLE pgr_src ADD COLUMN label TEXT", "ALTER TABLE pgr_log ADD COLUMN label TEXT"},
			ddl:               `CREATE RULE r AS ON INSERT TO pgr_src DO INSERT INTO pgr_log VALUES(NEW.id,NEW.v,NEW.label) ON CONFLICT(label COLLATE pg_catalog."C" pg_catalog.text_pattern_ops) DO NOTHING`,
			definition:        "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log (id, v, label)\n  VALUES (new.id, new.v, new.label) ON CONFLICT(label COLLATE \"C\" text_pattern_ops) DO NOTHING;",
			extraRenames:      []string{"ALTER TABLE pgr_src RENAME COLUMN label TO source_label", "ALTER TABLE pgr_log_now RENAME COLUMN label TO log_label"},
			renamedDefinition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO pgr_log_now (log_id, log_v, log_label)\n  VALUES (new.source_id, new.source_v, new.source_label) ON CONFLICT(log_label COLLATE \"C\" text_pattern_ops) DO NOTHING;",
			offPathDefinition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  INSERT INTO public.pgr_log_now (log_id, log_v, log_label)\n  VALUES (new.source_id, new.source_v, new.source_label) ON CONFLICT(log_label COLLATE \"C\" text_pattern_ops) DO NOTHING;",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					for _, q := range []string{"CREATE TABLE pgr_src(id INT,v INT)", "CREATE TABLE pgr_log(id INT CONSTRAINT pgr_key PRIMARY KEY,v INT)"} {
						run(fixture.Exec(q, 0))
					}
					for _, q := range c.setup {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec(c.ddl, 0))
					pgRulesCheck(t, p, b, conn, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.definition}}, "public", "pgr_src", "r")
					if c.quotedDefinition != "" {
						run(fixture.Exec("SET quote_all_identifiers TO on", 0))
						pgRulesCheck(t, p, b, conn, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.quotedDefinition}}, "public", "pgr_src", "r")
						run(fixture.Exec("RESET quote_all_identifiers", 0))
					}
					for _, q := range []string{"ALTER TABLE pgr_log RENAME TO pgr_log_now", "ALTER TABLE pgr_log_now RENAME COLUMN id TO log_id", "ALTER TABLE pgr_log_now RENAME COLUMN v TO log_v", "ALTER TABLE pgr_src RENAME COLUMN id TO source_id", "ALTER TABLE pgr_src RENAME COLUMN v TO source_v"} {
						run(fixture.Exec(q, 0))
					}
					for _, q := range c.extraRenames {
						run(fixture.Exec(q, 0))
					}
					pgRulesCheck(t, p, b, conn, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.renamedDefinition}}, "public", "pgr_src", "r")
					if c.offPathDefinition != "" {
						run(fixture.Exec("SET search_path TO pg_catalog", 0))
						pgRulesCheck(t, p, b, conn, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.offPathDefinition}}, "public", "pgr_src", "r")
						run(fixture.Exec("RESET search_path", 0))
					}
					for _, table := range []string{"pgr_src", "pgr_log_now"} {
						run(fixture.Select("SELECT count(*) FROM "+table, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
				})
			}
		})
	}
}
