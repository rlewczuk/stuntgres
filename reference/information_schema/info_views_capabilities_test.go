package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewsCapabilities(t *testing.T) {
	for _, tc := range []struct{ name, query, update, insert string }{
		{"simple", "SELECT id,note FROM iv_cap_base", "YES", "YES"},
		{"constant", "SELECT 1 AS n", "NO", "NO"},
		{"computed", "SELECT id+1 AS n FROM iv_cap_base", "NO", "NO"},
		{"mixed", "SELECT id,id+1 AS n FROM iv_cap_base", "YES", "YES"},
		{"scalar", "SELECT id,(SELECT 1) AS n FROM iv_cap_base", "YES", "YES"},
		{"join", "SELECT a.id FROM iv_cap_base a JOIN iv_cap_other b ON a.id=b.id", "NO", "NO"},
		{"aggregate", "SELECT count(*) AS n FROM iv_cap_base", "NO", "NO"},
		{"group", "SELECT id FROM iv_cap_base GROUP BY id", "NO", "NO"},
		{"distinct", "SELECT DISTINCT id FROM iv_cap_base", "NO", "NO"},
		{"set", "SELECT id FROM iv_cap_base UNION ALL SELECT id FROM iv_cap_other", "NO", "NO"},
		{"window", "SELECT id,row_number() OVER () AS n FROM iv_cap_base", "NO", "NO"},
		{"srf", "SELECT id,generate_series(1,2) AS n FROM iv_cap_base", "NO", "NO"},
		{"limit", "SELECT id FROM iv_cap_base LIMIT 1", "NO", "NO"},
		{"offset", "SELECT id FROM iv_cap_base OFFSET 1", "NO", "NO"},
		{"with", "WITH x AS (SELECT 1) SELECT id FROM iv_cap_base", "NO", "NO"},
		{"nested_direct", "SELECT id FROM iv_cap_inner", "YES", "YES"},
		{"nested_computed", "SELECT n FROM iv_cap_inner", "NO", "NO"},
		{"nested_constant", "SELECT 1 AS n FROM iv_cap_inner", "NO", "NO"},
		{"partition", "SELECT id FROM iv_cap_partition", "YES", "YES"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{"CREATE TABLE iv_cap_base(id integer,note text)", "CREATE TABLE iv_cap_other(id integer)", "CREATE TABLE iv_cap_partition(id integer) PARTITION BY RANGE(id)", "CREATE VIEW iv_cap_inner AS SELECT id,id+1 AS n FROM iv_cap_base", "CREATE VIEW iv_cap AS " + tc.query} {
					run(fixture.Exec(q, 0))
				}
				s := ivFlags(ivFlagSelect, ivFlagRow("iv_cap", tc.update, tc.insert), "iv_cap")
				run(s)
				run(fixture.Prepared(s))
				ivHeld(t, p, b, c, ivFlagSelect)(s)
			})
		})
	}
	t.Run("stable_subset_and_select_only", func(t *testing.T) {
		ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ string) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"CREATE ROLE iv_reader", "CREATE TABLE iv_stable_base(unused integer,id integer)",
				"CREATE VIEW iv_stable_inner AS SELECT id,id+1 AS n FROM iv_stable_base",
				"CREATE VIEW iv_stable_direct AS SELECT id FROM iv_stable_inner",
				"CREATE VIEW iv_stable_computed AS SELECT n FROM iv_stable_inner",
				"GRANT SELECT ON iv_stable_direct,iv_stable_computed TO iv_reader",
			} {
				run(fixture.Exec(q, 0))
			}
			held := ivHeld(t, p, b, c, ivFlagSelect)
			check := func() {
				for _, tc := range []struct{ name, flag string }{{"iv_stable_direct", "YES"}, {"iv_stable_computed", "NO"}} {
					s := ivFlags(ivFlagSelect, ivFlagRow(tc.name, tc.flag, tc.flag), tc.name)
					run(s)
					run(fixture.Prepared(s))
					held(s)
				}
			}
			check()
			run(fixture.Exec("ALTER TABLE iv_stable_base DROP COLUMN unused", 0))
			run(fixture.Exec("ALTER TABLE iv_stable_base RENAME COLUMN id TO renamed", 0))
			check()
			run(fixture.Exec("SET SESSION AUTHORIZATION iv_reader", 0))
			check()
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	})
}

func TestReferenceInfoViewsRules(t *testing.T) {
	for _, tc := range []struct {
		name           string
		events         []string
		update, insert string
	}{
		{"none", nil, "NO", "NO"}, {"insert", []string{"INSERT"}, "NO", "YES"},
		{"update", []string{"UPDATE"}, "NO", "NO"}, {"delete", []string{"DELETE"}, "NO", "NO"},
		{"update_delete", []string{"UPDATE", "DELETE"}, "YES", "NO"},
		{"all", []string{"INSERT", "UPDATE", "DELETE"}, "YES", "YES"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE VIEW iv_rule AS SELECT 1 AS n", 0))
				held := ivHeld(t, p, b, c, ivFlagSelect)
				check := func(update, insert string) {
					s := ivFlags(ivFlagSelect, ivFlagRow("iv_rule", update, insert), "iv_rule")
					run(s)
					run(fixture.Prepared(s))
					held(s)
				}
				check("NO", "NO")
				for i, event := range tc.events {
					run(fixture.Exec(fmt.Sprintf("CREATE RULE iv_rule_%d AS ON %s TO iv_rule DO INSTEAD NOTHING", i, event), 0))
				}
				check(tc.update, tc.insert)
				for i := range tc.events {
					run(fixture.Exec(fmt.Sprintf("DROP RULE iv_rule_%d ON iv_rule", i), 0))
				}
				check("NO", "NO")
				// A condition and DO ALSO do not add unconditional events.
				run(fixture.Exec("CREATE RULE iv_rule_conditional AS ON UPDATE TO iv_rule WHERE OLD.n>0 DO INSTEAD NOTHING", 0))
				run(fixture.Exec("CREATE RULE iv_rule_also AS ON DELETE TO iv_rule DO ALSO NOTHING", 0))
				check("NO", "NO")
			})
		})
	}
}
