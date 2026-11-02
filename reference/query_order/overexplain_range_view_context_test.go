package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainRangeViewDemandContexts(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"range_demand_owner", "range_demand_actor"})
	overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE ROLE range_demand_owner", "CREATE ROLE range_demand_actor",
			"GRANT USAGE ON SCHEMA public TO range_demand_owner,range_demand_actor",
			"CREATE TABLE range_demand_base(id integer,v text)",
			"GRANT SELECT(id,v),UPDATE(v) ON range_demand_base TO range_demand_owner",
			"CREATE VIEW range_demand_plain AS SELECT id,v FROM range_demand_base",
			"CREATE VIEW range_demand_marked AS SELECT id,v FROM range_demand_base FOR UPDATE",
			"CREATE VIEW range_demand_oid_marked AS SELECT id,v,tableoid AS origin FROM range_demand_base FOR UPDATE",
			"ALTER VIEW range_demand_plain OWNER TO range_demand_owner",
			"ALTER VIEW range_demand_marked OWNER TO range_demand_owner",
			"ALTER VIEW range_demand_oid_marked OWNER TO range_demand_owner",
			"GRANT SELECT(id,v) ON range_demand_plain,range_demand_marked,range_demand_oid_marked TO range_demand_actor",
			"LOAD 'pg_overexplain'",
		} {
			overexplainExec(t, p, b, c, q)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO range_demand_base VALUES(1,'one'),(2,NULL)", 2))
		overexplainExec(t, p, b, c, "SET ROLE range_demand_actor")
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		cases := []struct {
			name, child, view                  string
			analysisOnly, validation, tableoid bool
		}{
			{"unused-unmarked", "WITH unused AS (SELECT id FROM range_demand_plain) SELECT 1", "range_demand_plain", true, false, false},
			{"unused-marked", "WITH unused AS (SELECT id FROM range_demand_marked) SELECT 1", "range_demand_marked", true, true, false},
			{"read-marked", "SELECT id FROM range_demand_marked ORDER BY id", "range_demand_marked", false, true, false},
			{"unused-tableoid-marked", "WITH unused AS (SELECT id FROM range_demand_oid_marked) SELECT 1", "range_demand_oid_marked", true, true, true},
			{"read-tableoid-marked", "SELECT id FROM range_demand_oid_marked ORDER BY id", "range_demand_oid_marked", false, true, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if tc.tableoid && !tc.analysisOnly {
					overexplainExec(t, p, b, c, "RESET ROLE")
					overexplainExec(t, p, b, c, "GRANT SELECT ON range_demand_base TO range_demand_owner")
					overexplainExec(t, p, b, c, "SET ROLE range_demand_actor")
				}
				q := overexplainSQL("JSON", "RANGE_TABLE, VERBOSE, COSTS FALSE", tc.child)
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				check := func(r overexplainResult) {
					entries := overexplainRange(t, b, r, "JSON", true)
					if b.PG {
						if tc.analysisOnly {
							for _, entry := range entries {
								if entry["Kind"] == "relation" {
									t.Fatal("PG unused SELECT CTE retained an executable relation range", entries)
								}
							}
						} else {
							found := false
							for _, entry := range entries {
								if entry["Relation"] == "public.range_demand_base" && entry["Relation Lock Mode"] == "RowShareLock" {
									if overexplainInteger(t, entry["Permission Info Index"], false) <= 0 {
										t.Fatal("PG marked base lost permission reference", entry)
									}
									found = true
								}
							}
							if !found {
								t.Fatal("PG marked view lost actual locked base range", entries)
							}
						}
						return
					}
					outer := overexplainRangeNamed(t, b, entries, "relation", tc.view)
					base := overexplainRangeNamed(t, b, entries, "relation", "range_demand_base")
					overexplainRangeRelation(t, p, c, outer, tc.view, "JSON")
					overexplainRangeRelation(t, p, c, base, "range_demand_base", "JSON")
					for _, entry := range []map[string]any{outer, base} {
						flag, exists := entry["Analysis Only"]
						if tc.analysisOnly && (!exists || flag != true) || !tc.analysisOnly && exists {
							t.Fatal("stored expansion lost actual analysis-only context", entry, tc.analysisOnly)
						}
					}
					outerWants := []overexplainRangeDemand{}
					if !tc.analysisOnly {
						outerWants = append(outerWants, overexplainRangeDemand{"SELECT", []string{"id"}, false})
					}
					overexplainRangeDemands(t, outer, "range_demand_actor", outerWants)
					baseWants := []overexplainRangeDemand{}
					if tc.tableoid && !tc.analysisOnly {
						baseWants = append(baseWants, overexplainRangeDemand{"SELECT", []string{}, true})
					}
					if tc.validation {
						baseWants = append(baseWants, overexplainRangeDemand{"SELECT", []string{"id", "v"}, false}, overexplainRangeDemand{"UPDATE", []string{}, false})
					}
					overexplainRangeDemands(t, base, "range_demand_owner", baseWants)
					outerScope := overexplainRangeOwnerScope(t, r, outer["Entry ID"])
					baseScope := overexplainRangeOwnerScope(t, r, base["Entry ID"])
					if baseScope["Kind"] != "view" || baseScope["Parent Scope ID"] != outerScope["Scope ID"] || baseScope["Owner Entry ID"] != outer["Entry ID"] {
						t.Fatal("lost stored owner expansion boundary", outerScope, baseScope)
					}
				}
				for _, prepared := range []bool{false, true} {
					check(overexplainQuery(t, p, c, q, "JSON", prepared))
				}
				for range 2 {
					rows, err := held.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					check(overexplainRows(t, rows, "JSON"))
				}
			})
		}
		// A real read still uses the definer's column and any-column UPDATE rights.
		values := fixture.Select("SELECT id,v FROM range_demand_marked ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, values)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(values))
		overexplainExec(t, p, b, c, "RESET ROLE")
		overexplainExec(t, p, b, c, "REVOKE UPDATE(v) ON range_demand_base FROM range_demand_owner")
		overexplainExec(t, p, b, c, "SET ROLE range_demand_actor")
		q := overexplainSQL("JSON", "RANGE_TABLE", "SELECT id FROM range_demand_marked")
		overexplainError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table range_demand_base"), "execute", true)
		overexplainExec(t, p, b, c, "RESET ROLE")
		overexplainExec(t, p, b, c, "GRANT UPDATE(v) ON range_demand_base TO range_demand_owner")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,v FROM range_demand_base ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}}))
	})
}
