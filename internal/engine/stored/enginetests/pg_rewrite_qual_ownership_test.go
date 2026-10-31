package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func TestPGRewriteQualNamespaceOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, condition string
	}{
		{"old", "EXISTS(SELECT 1 FROM qualifier_gate old WHERE old.v>0)"},
		{"new", "EXISTS(SELECT 1 FROM qualifier_gate new WHERE new.v>0)"},
		{"whole_old", "EXISTS(SELECT 1 FROM qualifier_gate old WHERE old.* IS NOT NULL)"},
		{"whole_new", "EXISTS(SELECT 1 FROM qualifier_gate new WHERE new.* IS NOT NULL)"},
		{"outer_alias", "EXISTS(SELECT 1 FROM qualifier_gate old WHERE EXISTS(SELECT 1 WHERE old.v>0))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			session := enginetest.StartSession(t, e, nil)
			for _, query := range []string{
				"CREATE TABLE qualifier_event(v integer)",
				"CREATE TABLE qualifier_gate(v integer)",
				"CREATE RULE r AS ON UPDATE TO qualifier_event WHERE " + tc.condition + " DO ALSO NOTHING",
			} {
				enginetest.SessionSQL(t, session, query)
			}
			read := e.CatalogRead()
			owner := read.MustRelation(enginetest.CatalogRef(t, e, read, "qualifier_event").Address)
			gate := enginetest.CatalogRef(t, e, read, "qualifier_gate").Address
			condition := owner.Rules.(stored.RuleList)[0].Parts().Condition
			columns := 0
			var rows []plan.RowExpr
			var inspectQuery func(plan.Node)
			var inspectExpression func(expr.Expr)
			inspectExpression = func(expression expr.Expr) {
				plan.WalkExpression(expression, func(node expr.Expr) {
					switch n := node.(type) {
					case plan.ColumnExpr:
						columns++
						if n.Scope == analyze.RulePseudoScope || n.Origin.Address.ObjectID != gate.ObjectID || n.Origin.Address.SubID != 1 {
							t.Fatal("local alias captured a rule pseudo owner", n, gate)
						}
					case plan.RowExpr:
						rows = append(rows, n)
						if n.RuleSide != 0 {
							t.Fatal("local whole row captured a rule pseudo owner", n)
						}
					case plan.SubqueryExpr:
						if n.QueryPlan != nil {
							inspectQuery(n.QueryPlan.Root)
						}
					}
				})
			}
			inspectQuery = func(node plan.Node) {
				if node == nil {
					return
				}
				plan.QueryExpressions(node, inspectExpression)
				left, right := plan.QueryInputs(node)
				inspectQuery(left)
				inspectQuery(right)
			}
			inspectExpression(condition)
			if columns == 0 {
				t.Fatal("no genuine local column owner inspected")
			}
			if len(rows) != 0 {
				original := append([]plan.RowExpr(nil), rows...)
				frame := func(value uint32) uint32 { return value + 10 }
				mapped, changed, err := stored.NewRuleTreeMapper(read, frame, 0, nil).Expression(condition)
				if err != nil || !changed {
					t.Fatal("whole-row query frame mapping failed", changed, err)
				}
				rows = rows[:0]
				inspectExpression(mapped)
				if len(rows) != len(original) {
					t.Fatal("whole-row occurrence lost during mapping", len(rows), len(original))
				}
				for i, before := range original {
					after := rows[i]
					if before.PresenceFrame == 0 || before.WholeSource.Frame != before.PresenceFrame || after.PresenceFrame != frame(before.PresenceFrame) || after.WholeSource.Frame != frame(before.WholeSource.Frame) || after.PresenceSlot != before.PresenceSlot || after.RuleSide != before.RuleSide {
						t.Fatal("whole-row source association failed to follow its query owner", before, after)
					}
					for j, oldField := range before.Fields {
						oldColumn, oldOK := oldField.(plan.ColumnExpr)
						newColumn, newOK := after.Fields[j].(plan.ColumnExpr)
						if !oldOK || !newOK || oldColumn.Frame != before.PresenceFrame || newColumn.Frame != after.PresenceFrame {
							t.Fatal("whole-row fields detached from source frame or original mutated", oldField, after.Fields[j], before, after)
						}
					}
				}
			}
		})
	}
}
