package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func overexplainNativeTargets(t *testing.T, r overexplainResult, fragments ...string) {
	t.Helper()
	for _, node := range explainJSONNodes(r.document) {
		d, ok := node["Embedded Debug"].(map[string]any)
		if !ok {
			continue
		}
		targets, ok := d["Target List"].([]any)
		if !ok || len(targets) != len(fragments) {
			continue
		}
		match := true
		for i, fragment := range fragments {
			text, ok := targets[i].(string)
			if !ok || !strings.Contains(text, fragment) {
				match = false
			}
		}
		if match {
			return
		}
	}
	t.Fatalf("missing ordered genuine target list %q in %v", fragments, r.document)
}

func overexplainPlanIDs(t *testing.T, b fixture.Backend, r overexplainResult) {
	t.Helper()
	seen := make(map[int64]bool)
	for _, node := range explainJSONNodes(r.document) {
		var id int64
		if b.PG {
			id = overexplainInteger(t, node["Plan Node ID"], false)
		} else {
			d, ok := node["Embedded Debug"].(map[string]any)
			if !ok {
				// Synthetic presentation wrappers must not invent bound identities.
				continue
			}
			id = overexplainInteger(t, d["Node ID"], false)
		}
		// The selected fixtures contain no repeated bound node. More general
		// graph reuse/identity correspondence is an actual-owner unit obligation.
		if seen[id] {
			t.Fatalf("distinct fixture nodes aliased ID %d: %v", id, r.document)
		}
		seen[id] = true
	}
	if len(seen) == 0 {
		t.Fatal("no node identities")
	}
}

func TestReferenceOverexplainPlans(t *testing.T) {
	cases := []struct {
		name, child, command string
		nativeTargets        []string
	}{
		{"scan-filter-sort-project", "SELECT id,coalesce(v,'a,b') FROM overexplain_data WHERE id>1 ORDER BY id DESC", "select", []string{"id", "COALESCE"}},
		{"self-join", "SELECT l.id,r.v FROM overexplain_data l JOIN overexplain_data r ON l.id=r.id ORDER BY l.id", "select", []string{"l.id", "r.v"}},
		{"cte", "WITH x AS MATERIALIZED (SELECT id,v FROM overexplain_data) SELECT x.id,x.v FROM x ORDER BY x.id", "select", []string{"x.id", "x.v"}},
		{"derived", "SELECT d.id,d.v FROM (SELECT id,v FROM overexplain_data) d ORDER BY d.id", "select", []string{"d.id", "d.v"}},
		{"duplicate-derived", "SELECT d.id,d.v FROM overexplain_data d CROSS JOIN (VALUES(1),(1)) repeats(k) ORDER BY d.id", "select", []string{"d.id", "d.v"}},
		{"correlated", "SELECT o.id,(SELECT i.v FROM overexplain_data i WHERE i.id=o.id) FROM overexplain_data o ORDER BY o.id", "select", []string{"o.id", "SubPlan"}},
		{"insert-alias", "INSERT INTO overexplain_data AS t SELECT s.id,s.v FROM overexplain_source s RETURNING t.id,t.v", "insert", nil},
		{"update-source", "UPDATE overexplain_data t SET v=s.v FROM overexplain_source s WHERE t.id=s.id RETURNING s.v,t.id", "update", nil},
		{"delete-source", "DELETE FROM overexplain_data t USING overexplain_source s WHERE t.id=s.id RETURNING s.v,t.id", "delete", nil},
		{"merge-source", "MERGE INTO overexplain_data t USING overexplain_source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v RETURNING s.v,t.id", "merge", nil},
		{"modifying-cte", "WITH changed AS (UPDATE overexplain_data SET v='changed' RETURNING id) SELECT id FROM changed", "select", nil},
		{"volatile-nonexecution", "SELECT nextval('overexplain_seq'),id FROM overexplain_data ORDER BY id", "select", []string{"nextval", "id"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				overexplainSeed(t, p, b, c)
				overexplainExec(t, p, b, c, "CREATE TABLE overexplain_source(id integer,v text)")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO overexplain_source VALUES(1,'source'),(4,NULL)", 2))
				overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
				var relationOID int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='overexplain_data' AND relnamespace='public'::regnamespace::oid").Scan(&relationOID); err != nil || relationOID <= 0 {
					t.Fatal("actual relation identity", relationOID, err)
				}
				q := overexplainSQL("JSON", "DEBUG, VERBOSE, COSTS FALSE", tc.child)
				overexplainRoutes(t, p, c, q, "JSON", func(r overexplainResult) {
					overexplainDebug(t, b, r, "JSON", true)
					if b.PG {
						for _, entry := range r.document {
							stmt := entry["PlannedStmt"].(map[string]any)
							if stmt["Command Type"] != tc.command {
								t.Fatalf("PG command type: %v", stmt)
							}
							if tc.command != "select" && !strings.Contains(stmt["Flags"].(string), "hasReturning") {
								t.Fatal("RETURNING flag lost", stmt)
							}
							if tc.name == "modifying-cte" && !strings.Contains(stmt["Flags"].(string), "hasModifyingCTE") {
								t.Fatal("modifying CTE flag lost", stmt)
							}
							if !strings.Contains(stmt["Relation OIDs"].(string), fmt.Sprint(relationOID)) {
								t.Fatal("PG omitted real relation identity", stmt)
							}
						}
					} else {
						if tc.nativeTargets != nil {
							// Native subplan names/target SQL are nonportable; require
							// actual lexical ownership, not PostgreSQL expressions.
							if tc.name != "correlated" {
								overexplainNativeTargets(t, r, tc.nativeTargets...)
							} else {
								explainJSONContains(t, r.document, "o.id", "i.v", "SubPlan")
							}
						}
						if tc.name == "scan-filter-sort-project" {
							for _, typ := range []string{"Project", "Sort", "Filter", "Logical Scan"} {
								explainJSONFind(t, r.document, "Node Type", typ)
							}
							overexplainNativeTargets(t, r, "id", "a,b")
							foundSort := false
							for _, n := range explainJSONNodes(r.document) {
								if d, ok := n["Embedded Debug"].(map[string]any); ok {
									if keys, ok := d["Sort Targets"].([]any); ok && len(keys) == 1 {
										explainJSONContains(t, keys[0], "id")
										foundSort = true
									}
								}
							}
							if !foundSort {
								t.Fatal("actual sort key records absent")
							}
						}
						if tc.name == "self-join" {
							occurrences, ids := map[int64]bool{}, map[int64]bool{}
							for _, n := range explainJSONNodes(r.document) {
								if n["Relation Name"] != "overexplain_data" {
									continue
								}
								d, ok := n["Embedded Debug"].(map[string]any)
								if !ok {
									t.Fatal("source debug absent", n)
								}
								if overexplainInteger(t, d["Relation OID"], false) != relationOID {
									t.Fatal("not the actual source identity", d)
								}
								occurrences[overexplainInteger(t, d["Occurrence"], false)] = true
								ids[overexplainInteger(t, d["Node ID"], false)] = true
							}
							if len(occurrences) != 2 || len(ids) != 2 {
								t.Fatal("self-join occurrences/IDs aliased", occurrences, ids)
							}
						}
						if tc.command != "select" {
							lines := overexplainQuery(t, p, c, overexplainSQL("TEXT", "DEBUG, VERBOSE, COSTS FALSE", tc.child), "TEXT", false)
							var returning string
							for _, line := range strings.Split(lines.text, "\n") {
								if strings.HasPrefix(strings.TrimSpace(line), "RETURNING:") {
									returning = line
									break
								}
							}
							if tc.command == "insert" {
								explainContains(t, []string{returning}, "t.id", "t.v")
							} else {
								explainContains(t, []string{returning}, "s.v", "t.id")
							}
						}
					}
					if tc.name == "scan-filter-sort-project" || tc.name == "self-join" {
						overexplainPlanIDs(t, b, r)
					}
				})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,v FROM overexplain_source ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "source"}, {int64(4), nil}}))
				if tc.name == "self-join" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(tc.child, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}))
				}
				if tc.name == "duplicate-derived" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(tc.child, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(1), "one"}, {int64(2), nil}, {int64(2), nil}, {int64(3), "three"}, {int64(3), "three"}}))
				}
				overexplainInvariant(t, p, b, c)
			})
		})
	}
	t.Run("simple-postgresql-schema", func(t *testing.T) {
		overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
			overexplainRoutes(t, p, c, "EXPLAIN (DEBUG, FORMAT JSON, COSTS FALSE) SELECT 1", "JSON", func(r overexplainResult) {
				overexplainDebug(t, b, r, "JSON", true)
				if !b.PG {
					overexplainNativeTargets(t, r, "1")
					return
				}
				stmt := r.document[0]["PlannedStmt"].(map[string]any)
				for key, want := range map[string]string{"Command Type": "select", "Flags": "canSetTag", "Subplans Needing Rewind": "none", "Relation OIDs": "none", "Executor Parameter Types": "none"} {
					if stmt[key] != want {
						t.Fatalf("simple PG %s=%v want %s", key, stmt[key], want)
					}
				}
				for _, node := range explainJSONNodes(r.document) {
					if node["extParam"] != "none" || node["allParam"] != "none" {
						t.Fatal("invented executor params", node)
					}
				}
			})
		})
	})
}
