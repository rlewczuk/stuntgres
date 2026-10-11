package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExprCollationHashDemand(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		lhs := `CASE WHEN a=b THEN TRUE ELSE FALSE END`
		for _, op := range []string{"= ANY", "IN", "NOT IN"} {
			empty := `SELECT id,` + lhs + ` ` + op + ` (SELECT TRUE FROM expr_coll_t WHERE id<0) AS v FROM expr_coll_t WHERE id<=2 ORDER BY id`
			want := op == "NOT IN"
			fixture.CollationRoutes(t, p, b, fixture.Select(empty, []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), want}, {int64(2), want}}))
			for _, rhs := range []string{`TRUE`, `NULL::BOOL`} {
				// A nonempty NULL-only hash table still demands the LHS at
				// execution before yielding UNKNOWN.
				q := `SELECT ` + lhs + ` ` + op + ` (SELECT ` + rhs + ` FROM expr_coll_t WHERE id=1) AS v FROM expr_coll_t WHERE id=1`
				fixture.CollationRoutes(t, p, b, exprCollationDemand(q))
			}
		}
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT NULL::BOOL = ANY(SELECT TRUE FROM expr_coll_t WHERE id<0) AS v`, []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT NULL::BOOL NOT IN(SELECT TRUE FROM expr_coll_t WHERE id<0) AS v`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT id,CASE WHEN a=b THEN TRUE ELSE FALSE END = ANY(SELECT NULL::BOOL FROM expr_coll_t WHERE id=1) AS v FROM expr_coll_t WHERE id>=3 ORDER BY id`, []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(3), nil}, {int64(4), nil}, {int64(5), nil}}))
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT r.id,(l.id,l.a COLLATE "C") IN(SELECT id,b FROM expr_coll_t WHERE id=4) AS v FROM expr_coll_t l JOIN expr_coll_t r ON r.id<=2 WHERE l.id=4 ORDER BY r.id`, []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), nil}, {int64(2), nil}}))
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT id,(id,a COLLATE "C") NOT IN(SELECT id,b FROM expr_coll_t WHERE id=4) AS v FROM expr_coll_t WHERE id<=2 ORDER BY id`, []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), true}}))
		// Each route owns fresh effect state, so no route-count inference can
		// hide evaluation before an empty RHS or skipped NULL-only demand.
		for _, prepared := range []bool{false, true} {
			for _, nonContext := range []bool{false, true} {
				for _, tc := range []struct {
					name, op, rhs string
					want          any
					calls         int64
				}{
					{"empty", "= ANY", `SELECT TRUE FROM expr_coll_t WHERE id<0`, false, 0},
					{"negempty", "NOT IN", `SELECT TRUE FROM expr_coll_t WHERE id<0`, true, 0},
					{"nonnull", "= ANY", `SELECT TRUE FROM expr_coll_t WHERE id=1`, true, 2},
					{"null", "= ANY", `SELECT NULL::BOOL FROM expr_coll_t WHERE id=1`, nil, 2},
				} {
					name := "expr_hash_lhs_" + tc.name + "_" + strconv.FormatBool(prepared) + "_" + strconv.FormatBool(nonContext)
					fixture.CollationExec(t, p, b, `CREATE SEQUENCE `+name)
					q := `SELECT id,CASE WHEN nextval('` + name + `')>id*0 THEN TRUE ELSE FALSE END ` + tc.op + ` (` + tc.rhs + `) AS v FROM expr_coll_t WHERE id<=2 ORDER BY id`
					s := fixture.Select(q, []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), tc.want}, {int64(2), tc.want}})
					s.Prepared, s.NonContext = prepared, nonContext
					fixture.CollationStep(t, p, b, s)
					last := tc.calls
					if last == 0 {
						last = 1
					}
					fixture.CollationStep(t, p, b, fixture.Select(`SELECT last_value,is_called FROM `+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{last, tc.calls != 0}}))
				}
				name := "expr_hash_rhs_error_" + strconv.FormatBool(prepared) + "_" + strconv.FormatBool(nonContext)
				fixture.CollationExec(t, p, b, `CREATE SEQUENCE `+name)
				q := `SELECT CASE WHEN nextval('` + name + `')>0 THEN a ELSE NULL::TEXT END = ANY(SELECT b FROM expr_coll_t WHERE id=1) AS v FROM expr_coll_t WHERE id=1`
				demand := exprCollationDemand(q)
				demand.Diagnostic.Message = "could not determine which collation to use for string hashing"
				demand.Prepared, demand.NonContext = prepared, nonContext
				fixture.CollationStep(t, p, b, demand)
				fixture.CollationStep(t, p, b, fixture.Select(`SELECT last_value,is_called FROM `+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
			}
		}
	})
}

func TestReferenceExprCollationHashInvocation(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		for _, prepared := range []bool{false, true} {
			for _, nonContext := range []bool{false, true} {
				route := strconv.FormatBool(prepared) + "_" + strconv.FormatBool(nonContext)
				for _, tc := range []struct {
					name, expression string
					want             [][]any
					calls            int64
				}{
					{"once", `id::TEXT COLLATE "C" = ANY(SELECT nextval('SEQ')::TEXT COLLATE "C")`, [][]any{{int64(1), true}, {int64(2), false}}, 1},
					{"correlated", `o.id::TEXT COLLATE "C" = ANY(SELECT nextval('SEQ')::TEXT COLLATE "C" FROM expr_coll_t i WHERE i.id=o.id)`, [][]any{{int64(1), true}, {int64(2), true}}, 2},
					// Unchanged child dependencies retain the RHS across
					// rescans of its correlated parent, in both query forms.
					{"nested", `EXISTS(SELECT 1 FROM expr_coll_t i WHERE i.id=o.id AND i.id::TEXT COLLATE "C" = ANY(SELECT nextval('SEQ')::TEXT COLLATE "C"))`, [][]any{{int64(1), true}, {int64(2), false}}, 1},
					// OFFSET 0 retains the ordinary correlated EXISTS route,
					// rather than allowing its simplification to hashed EXISTS.
					{"nested_rescan", `EXISTS(SELECT 1 FROM expr_coll_t i WHERE i.id=o.id AND i.id::TEXT COLLATE "C" = ANY(SELECT nextval('SEQ')::TEXT COLLATE "C") OFFSET 0)`, [][]any{{int64(1), true}, {int64(2), false}}, 1},
				} {
					name := "expr_hash_rhs_" + tc.name + "_" + route
					fixture.CollationExec(t, p, b, `CREATE SEQUENCE `+name)
					expression := strings.Replace(tc.expression, "SEQ", name, 1)
					q := `SELECT o.id,` + expression + ` AS v FROM expr_coll_t o WHERE o.id<=2 ORDER BY o.id`
					s := fixture.Select(q, []string{"id", "v"}, []string{"INT4", "BOOL"}, tc.want)
					s.Prepared, s.NonContext = prepared, nonContext
					fixture.CollationStep(t, p, b, s)
					fixture.CollationStep(t, p, b, fixture.Select(`SELECT last_value,is_called FROM `+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{tc.calls, true}}))
				}
				name := "expr_hash_occurrence_" + route
				fixture.CollationExec(t, p, b, `CREATE SEQUENCE `+name)
				q := `SELECT id,id::TEXT COLLATE "C" = ANY(SELECT nextval('` + name + `')::TEXT COLLATE "C") AS v,id::TEXT COLLATE "C" = ANY(SELECT nextval('` + name + `')::TEXT COLLATE "C") AS w FROM expr_coll_t WHERE id<=2 ORDER BY id`
				s := fixture.Select(q, []string{"id", "v", "w"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, false}, {int64(2), false, true}})
				s.Prepared, s.NonContext = prepared, nonContext
				fixture.CollationStep(t, p, b, s)
				fixture.CollationStep(t, p, b, fixture.Select(`SELECT last_value,is_called FROM `+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
			}
		}
		for _, nonContext := range []bool{false, true} {
			name := "expr_hash_held_" + strconv.FormatBool(nonContext)
			fixture.CollationExec(t, p, b, `CREATE SEQUENCE `+name)
			q := `SELECT id,id::TEXT COLLATE "C" = ANY(SELECT (nextval('` + name + `')+$1::BIGINT)::TEXT COLLATE "C") AS v FROM expr_coll_t WHERE id<=2 ORDER BY id`
			var stmt *sql.Stmt
			var err error
			if nonContext {
				stmt, err = b.DB.Prepare(q)
			} else {
				stmt, err = b.DB.PrepareContext(p.Ctx, q)
			}
			if err != nil {
				t.Fatal("quiet held hash Prepare", err)
			}
			defer stmt.Close()
			fixture.CollationStep(t, p, b, fixture.Select(`SELECT last_value,is_called FROM `+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
			for i, tc := range []struct {
				arg           int64
				first, second bool
			}{{0, true, false}, {0, false, true}, {10, false, false}, {-2, false, true}} {
				var rows *sql.Rows
				if nonContext {
					rows, err = stmt.Query(tc.arg)
				} else {
					rows, err = stmt.QueryContext(p.Ctx, tc.arg)
				}
				fixture.DeleteReferenceConsume(t, b.PG, rows, err, fixture.Select(q, []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), tc.first}, {int64(2), tc.second}}, tc.arg))
				fixture.CollationStep(t, p, b, fixture.Select(`SELECT last_value,is_called FROM `+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(i + 1), true}}))
			}
		}
	})
}

func TestReferenceExprCollationHashCTEInvocation(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		for _, prepared := range []bool{false, true} {
			for _, nonContext := range []bool{false, true} {
				for _, tc := range []struct {
					name, query string
					second      bool
					calls       int64
				}{
					{"top", `WITH c AS MATERIALIZED(SELECT nextval('SEQ')::TEXT COLLATE "C" AS n) SELECT o.id,o.id::TEXT COLLATE "C" = ANY(SELECT n FROM c) AS v FROM expr_coll_t o WHERE o.id<=2 ORDER BY o.id`, false, 1},
					// The unchanged CTE-backed RHS survives physical parent
					// rescans; only actual outer dependencies require freshness.
					{"parent", `SELECT o.id,EXISTS(WITH c AS MATERIALIZED(SELECT nextval('SEQ')::TEXT COLLATE "C" AS n) SELECT 1 FROM expr_coll_t i WHERE i.id=o.id AND i.id::TEXT COLLATE "C" = ANY(SELECT n FROM c) OFFSET 0) AS v FROM expr_coll_t o WHERE o.id<=2 ORDER BY o.id`, false, 1},
					{"correlated", `SELECT o.id,o.id::TEXT COLLATE "C" = ANY(WITH c AS MATERIALIZED(SELECT nextval('SEQ')::TEXT COLLATE "C" AS n FROM expr_coll_t i WHERE i.id=o.id) SELECT n FROM c) AS v FROM expr_coll_t o WHERE o.id<=2 ORDER BY o.id`, true, 2},
				} {
					name := "expr_hash_cte_" + tc.name + "_" + strconv.FormatBool(prepared) + "_" + strconv.FormatBool(nonContext)
					fixture.CollationExec(t, p, b, `CREATE SEQUENCE `+name)
					q := strings.Replace(tc.query, "SEQ", name, 1)
					s := fixture.Select(q, []string{"id", "v"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), tc.second}})
					s.Prepared, s.NonContext = prepared, nonContext
					fixture.CollationStep(t, p, b, s)
					fixture.CollationStep(t, p, b, fixture.Select(`SELECT last_value,is_called FROM `+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{tc.calls, true}}))
				}
			}
		}
	})
}

func TestReferenceExprCollationHashSourceOccurrence(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		for _, prepared := range []bool{false, true} {
			for _, nonContext := range []bool{false, true} {
				route := strconv.FormatBool(prepared) + "_" + strconv.FormatBool(nonContext)
				name, view := "expr_hash_alias_seq_"+route, "expr_hash_alias_v_"+route
				fixture.CollationExec(t, p, b, `CREATE SEQUENCE `+name, `CREATE VIEW `+view+` AS SELECT id,id::TEXT COLLATE "C" = ANY(SELECT nextval('`+name+`')::TEXT COLLATE "C") AS hit FROM expr_coll_t WHERE id<=2`)
				// Alias build order cannot affect the sum: each independent
				// occurrence matches one of IDs 1 and 2, and the IDs differ.
				q := `SELECT l.id+r.id AS total FROM ` + view + ` l JOIN ` + view + ` r ON l.id<>r.id WHERE l.hit AND r.hit`
				s := fixture.Select(q, []string{"total"}, []string{"INT4"}, [][]any{{int64(3)}})
				s.Prepared, s.NonContext = prepared, nonContext
				fixture.CollationStep(t, p, b, s)
				fixture.CollationStep(t, p, b, fixture.Select(`SELECT last_value,is_called FROM `+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
			}
		}
	})
}
