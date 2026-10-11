package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDependExpressionEdges(t *testing.T) {
	// PG18 discovery records analyzed dependencies before execution folding.
	// Isolated probes keep a discrepancy from hiding other owner categories.
	for _, owner := range []string{"default", "check", "generated", "index", "view"} {
		for _, probe := range []string{"repeat", "true", "false", "dead_regclass", "domain_const", "domain_var", "tableoid"} {
			if owner == "default" && (probe == "repeat" || probe == "domain_var" || probe == "tableoid") {
				continue
			}
			if probe == "tableoid" && owner != "view" && owner != "generated" {
				continue
			}
			t.Run(owner+"/"+probe, func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "pgdep_expr")
				p := fixture.NewScenario(t)
				fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
					fixture.PgDependExec(t, p, b, "CREATE SCHEMA pgdep_expr", "SET search_path TO pgdep_expr,public", "CREATE DOMAIN d AS INT", "CREATE TABLE provider(a INT)")
					provider := fixture.PgDependRelation(t, b, "provider")
					domain := fixture.PgDependID(t, b, "SELECT objid FROM pg_catalog.pg_depend WHERE classid=1247 AND refclassid=2615 AND refobjid=$1", fixture.PgDependNamespace(t, b, provider))
					expr := "a+a"
					switch probe {
					case "true":
						expr = "CASE WHEN TRUE THEN 1 ELSE 2 END"
					case "false":
						expr = "CASE WHEN FALSE THEN 1 ELSE 2 END"
					case "dead_regclass":
						expr = "CASE WHEN TRUE THEN TRUE ELSE 'provider'::regclass IS NOT NULL END"
					case "domain_const":
						expr = "1::d"
					case "domain_var":
						expr = "b"
					case "tableoid":
						expr = "tableoid"
					}
					definition := "a INT,b d"
					switch owner {
					case "default":
						definition += ",c BOOL DEFAULT ((" + expr + ") IS NOT NULL)"
					case "generated":
						definition += ",c BOOL GENERATED ALWAYS AS((" + expr + ") IS NOT NULL) STORED"
					case "check":
						definition += ",CONSTRAINT ck CHECK((" + expr + ") IS NOT NULL)"
					}
					fixture.PgDependExec(t, p, b, "CREATE TABLE t("+definition+")")
					table := fixture.PgDependRelation(t, b, "t")
					var refs [][]int64
					switch probe {
					case "repeat":
						refs = [][]int64{{1259, table, 1}}
					case "dead_regclass":
						refs = [][]int64{{1259, provider, 0}}
					case "domain_const":
						refs = [][]int64{{1247, domain, 0}}
					case "domain_var":
						refs = [][]int64{{1259, table, 2}}
					case "tableoid":
						refs = [][]int64{{1259, table, -6}}
					}
					var class, id int64
					var want [][]any
					switch owner {
					case "default":
						class = 2604
						id = fixture.PgDependDefinition(t, b, table, 3)
						want = [][]any{fixture.PgDependEdge(class, id, 0, 1259, table, 3, "a")}
					case "generated":
						class = 2604
						id = fixture.PgDependDefinition(t, b, table, 3)
						want = [][]any{fixture.PgDependEdge(class, id, 0, 1259, table, 3, "i")}
					case "check":
						class = 2606
						id = fixture.PgDependConstraint(t, b, table, "ck")
						sub := int64(0)
						if probe == "repeat" {
							sub = 1
						}
						if probe == "domain_var" {
							sub = 2
						}
						want = [][]any{fixture.PgDependEdge(class, id, 0, 1259, table, sub, "a")}
					case "index":
						fixture.PgDependExec(t, p, b, "CREATE INDEX ix ON t(("+expr+"))")
						class = 1259
						id = fixture.PgDependRelation(t, b, "ix")
						if probe != "domain_var" {
							want = append(want, fixture.PgDependEdge(class, id, 0, 1259, table, 0, "a"))
						}
					case "view":
						if probe == "tableoid" && !b.PG {
							fixture.PgDependError(t, p, b, "CREATE VIEW v AS SELECT tableoid AS c FROM t", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"tableoid\" does not exist", Position: "25"})
							return
						}
						fixture.PgDependExec(t, p, b, "CREATE VIEW v AS SELECT "+expr+" AS c FROM t")
						v := fixture.PgDependRelation(t, b, "v")
						class = 2618
						id = fixture.PgDependRule(t, b, v)
						want = [][]any{fixture.PgDependEdge(class, id, 0, 1259, v, 0, "i")}
						if probe != "repeat" && probe != "domain_var" && probe != "tableoid" {
							want = append(want, fixture.PgDependEdge(class, id, 0, 1259, table, 0, "n"))
						}
					}
					for _, ref := range refs {
						kind := "n"
						if owner == "index" && ref[0] == 1259 && ref[1] == table {
							kind = "a"
						}
						want = append(want, fixture.PgDependEdge(class, id, 0, ref[0], ref[1], ref[2], kind))
					}
					fixture.PgDependBag(t, p, b, class, id, want...)
				})
			})
		}
	}
	t.Run("query_frames", func(t *testing.T) {
		p := fixture.NewScenario(t)
		fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
			fixture.PgDependExec(t, p, b, "CREATE TABLE pgdep_q(a INT,b INT)", "CREATE TABLE pgdep_q2(a INT,b INT)")
			a := fixture.PgDependRelation(t, b, "pgdep_q")
			other := fixture.PgDependRelation(t, b, "pgdep_q2")
			cases := []struct {
				q    string
				refs [][]int64
			}{
				{"SELECT a+a AS c FROM pgdep_q WHERE b>0 ORDER BY a", [][]int64{{a, 1}, {a, 2}}},
				{"SELECT x.a FROM pgdep_q x JOIN pgdep_q2 y ON x.b=y.b", [][]int64{{a, 1}, {a, 2}, {other, 2}}},
				{"SELECT a,max(b) AS n FROM pgdep_q GROUP BY a HAVING max(b)>0", [][]int64{{a, 1}, {a, 2}}},
				{"SELECT z.a FROM (SELECT a FROM pgdep_q) z", [][]int64{{a, 1}}},
				{"SELECT a FROM pgdep_q UNION ALL SELECT a FROM pgdep_q2", [][]int64{{a, 1}, {other, 1}}},
				{"SELECT a FROM pgdep_q WHERE EXISTS(SELECT 1 FROM pgdep_q2 WHERE pgdep_q2.b=pgdep_q.b)", [][]int64{{a, 1}, {a, 2}, {other, 2}}},
			}
			for i, tc := range cases {
				name := fmt.Sprintf("pgdep_qv%d", i)
				fixture.PgDependExec(t, p, b, "CREATE VIEW "+name+" AS "+tc.q)
				v := fixture.PgDependRelation(t, b, name)
				r := fixture.PgDependRule(t, b, v)
				want := [][]any{fixture.PgDependEdge(2618, r, 0, 1259, v, 0, "i")}
				for _, ref := range tc.refs {
					want = append(want, fixture.PgDependEdge(2618, r, 0, 1259, ref[0], ref[1], "n"))
				}
				fixture.PgDependBag(t, p, b, 2618, r, want...)
			}
		})
	})
}
