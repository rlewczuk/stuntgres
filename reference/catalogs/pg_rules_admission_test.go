package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// CURRENT OF and custom RETURNING sides are release-admission questions, not
// permission to extend the parser or execute cursors. Success and display below
// are source-derived candidates for the root's pinned-oracle decision stage.
func TestReferencePGRulesActionAdmission(t *testing.T) {
	cases := []struct {
		name, ddl, definition string
	}{
		{
			name:       "update_current_of",
			ddl:        "CREATE OR REPLACE RULE r AS ON UPDATE TO pgr_src DO UPDATE pgr_log SET v=1 WHERE CURRENT OF pgr_cursor",
			definition: "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO  UPDATE pgr_log SET v = 1\n  WHERE CURRENT OF pgr_cursor;",
		},
		{
			name:       "delete_current_of_quoted_cursor",
			ddl:        "CREATE OR REPLACE RULE r AS ON DELETE TO pgr_src DO DELETE FROM pgr_log WHERE CURRENT OF \"Rule Cursor\"",
			definition: "CREATE RULE r AS\n    ON DELETE TO public.pgr_src DO  DELETE FROM pgr_log\n  WHERE CURRENT OF \"Rule Cursor\";",
		},
		{
			name: "returning_target_sides_and_aliases",
			// Reversed input options also exercise canonical OLD-then-NEW order.
			ddl:        "CREATE OR REPLACE RULE r AS ON UPDATE TO pgr_src DO INSTEAD UPDATE pgr_log AS l SET v=NEW.v RETURNING WITH (NEW AS after, OLD AS before) before.id AS previous_id,after.v AS replacement_v",
			definition: "CREATE RULE r AS\n    ON UPDATE TO public.pgr_src DO INSTEAD  UPDATE pgr_log l SET v = new.v\n  RETURNING WITH (OLD AS before, NEW AS after) before.id AS previous_id,\n    after.v AS replacement_v;",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Separate scenarios keep direct/prepared CREATE admission independent;
			// no cursor transaction or live cursor is needed for stored metadata.
			for _, prepared := range []bool{false, true} {
				route := "direct"
				if prepared {
					route = "prepared"
				}
				t.Run(route, func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
							run(fixture.Exec("CREATE TABLE pgr_src(id INT,v INT)", 0))
							run(fixture.Exec("CREATE TABLE pgr_log(id INT,v INT)", 0))
							run(fixture.Exec("INSERT INTO pgr_src VALUES(7,70)", 1))
							run(fixture.Exec("INSERT INTO pgr_log VALUES(7,700)", 1))
							run(fixture.Exec("CREATE RULE r AS ON INSERT TO pgr_src DO NOTHING", 0))
							pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_src", "r", "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO NOTHING;"}}, "public", "pgr_src", "r")
							s := fixture.Exec(c.ddl, 0)
							if prepared {
								s = fixture.Prepared(s)
							}
							run(s)
							pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.definition}}, "public", "pgr_src", "r")
							run(fixture.Select("SELECT id,v FROM pgr_src", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(70)}}))
							run(fixture.Select("SELECT id,v FROM pgr_log", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(700)}}))
						})
					}
				})
			}
		})
	}

	// These source-grounded eligibility controls do not stand in for observing
	// admission of the unconditional INSTEAD side/alias candidate above.
	for _, c := range []struct {
		name, ddl, message string
	}{
		{
			name:    "conditional_returning_rejected_atomically",
			ddl:     "CREATE OR REPLACE RULE r AS ON UPDATE TO pgr_src WHERE NEW.id>0 DO INSTEAD UPDATE pgr_log SET v=NEW.v RETURNING id,v",
			message: "RETURNING lists are not supported in conditional rules",
		},
		{
			name:    "also_returning_rejected_atomically",
			ddl:     "CREATE OR REPLACE RULE r AS ON UPDATE TO pgr_src DO ALSO UPDATE pgr_log SET v=NEW.v RETURNING id,v",
			message: "RETURNING lists are not supported in non-INSTEAD rules",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE pgr_src(id INT,v INT)", 0))
					run(fixture.Exec("CREATE TABLE pgr_log(id INT,v INT)", 0))
					run(fixture.Exec("INSERT INTO pgr_src VALUES(7,70)", 1))
					run(fixture.Exec("INSERT INTO pgr_log VALUES(7,700)", 1))
					run(fixture.Exec("CREATE RULE r AS ON INSERT TO pgr_src DO NOTHING", 0))
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(c.ddl, fixture.Diagnostic{Code: "0A000", Message: c.message})
						if prepared {
							s = fixture.Prepared(s)
						}
						s.ErrorStage = "execute"
						run(s)
						pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_src", "r", "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO NOTHING;"}}, "public", "pgr_src", "r")
						run(fixture.Select("SELECT id,v FROM pgr_src", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(70)}}))
						run(fixture.Select("SELECT id,v FROM pgr_log", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(700)}}))
					}
				})
			}
		})
	}
}
