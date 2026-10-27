package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Independent PostgreSQL 18 settings contracts, not native registry output.
type plannerOtherSetting struct {
	name, kind, boot, min, max, enums, first, second string
}

var plannerOtherSettings = []plannerOtherSetting{
	{"constraint_exclusion", "enum", "partition", "", "", "{partition,on,off}", "on", "off"},
	{"cursor_tuple_fraction", "real", "0.1", "0", "1", "", "0.2", "0.3"},
	{"default_statistics_target", "integer", "100", "1", "10000", "", "2", "3"},
	{"from_collapse_limit", "integer", "8", "1", "2147483647", "", "2", "3"},
	{"jit", "bool", "on", "", "", "", "off", "on"},
	{"join_collapse_limit", "integer", "8", "1", "2147483647", "", "2", "3"},
	{"plan_cache_mode", "enum", "auto", "", "", "{auto,force_generic_plan,force_custom_plan}", "force_generic_plan", "force_custom_plan"},
	{"recursive_worktable_factor", "real", "10", "0.001", "1e+06", "", "2", "3"},
}

func TestReferencePlannerOtherSurface(t *testing.T) {
	physicalCases(t, []string{"planner_other_login"}, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE ROLE planner_other_login LOGIN PASSWORD 'role-core-fixture'", 0))
		ordinary := s.login("planner_other_login")
		for _, prepared := range []bool{false, true} {
			d := physicalDiagnostic("22023", `parameter "jit" requires a Boolean value`)
			ordinary.run(physicalError("SET jit='bogus'", d, prepared, false))
			ordinary.run(physicalError("SELECT set_config($1,$2,false)", d, prepared, true, "jit", "bogus"))
			ordinary.resourceValue("jit", "on", "on", "default", prepared)
		}
		for _, observer := range []*physicalCase{s, ordinary} {
			for _, d := range plannerOtherSettings {
				for _, prepared := range []bool{false, true} {
					observer.resourceValue(d.name, d.boot, d.boot, "default", prepared)
					step := fixture.Select("SELECT vartype,context,category,unit,min_val,max_val,enumvals::text,boot_val,reset_val,pending_restart FROM pg_settings WHERE name=$1", []string{"vartype", "context", "category", "unit", "min_val", "max_val", "enumvals", "boot_val", "reset_val", "pending_restart"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{d.kind, "user", "Query Tuning / Other Planner Options", nil, resourceNullable(d.min), resourceNullable(d.max), resourceNullable(d.enums), d.boot, d.boot, false}}, d.name)
					step.Prepared = prepared
					observer.run(step)
					observer.plannerCostSet(d.name, d.first, prepared)
					observer.resourceValue(d.name, d.first, d.first, "session", prepared)
					observer.run(fixture.Exec("RESET "+d.name, 0))
				}
			}
			rows, err := observer.conn.QueryContext(s.p.Ctx, "SHOW ALL")
			if err != nil {
				t.Fatal(err)
			}
			found := map[string]bool{}
			for rows.Next() {
				var name, value, description string
				if err := rows.Scan(&name, &value, &description); err != nil {
					t.Fatal(err)
				}
				for _, d := range plannerOtherSettings {
					if name == d.name {
						if found[name] || value != d.boot || description == "" {
							t.Fatal(name, value, description)
						}
						found[name] = true
					}
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if len(found) != len(plannerOtherSettings) {
				t.Fatal("SHOW ALL missing settings", found)
			}
		}
	})
}
