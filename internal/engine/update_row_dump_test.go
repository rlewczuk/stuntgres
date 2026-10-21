package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// Exercise analyzed source ownership through the production action renderer and
// canonical rule reanalysis. Public mutation/replay outcomes stay in reference.
func TestUpdateRowRuleReplay(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		grouped      bool
	}{
		{"grouped_target", "UPDATE t AS dst SET (a,b)=(WITH v AS MATERIALIZED (SELECT dst.id AS x) SELECT x,x+100 FROM v) WHERE dst.id=NEW.id AND OLD.id=NEW.id", true},
		{"scalar_target", "UPDATE t SET a=(WITH v AS MATERIALIZED (SELECT t.id AS x) SELECT x FROM v) WHERE t.id=NEW.id AND OLD.id=NEW.id", false},
		{"grouped_from", "UPDATE t SET (a,b)=(WITH v AS MATERIALIZED (SELECT src.x+t.id AS x) SELECT x,x+100 FROM v) FROM src WHERE t.id=NEW.id AND src.id=t.id AND OLD.id=NEW.id", true},
		{"grouped_derived_from", "UPDATE t SET (a,b)=(WITH v AS MATERIALIZED (SELECT src.x+t.id AS x) SELECT x,x+100 FROM v) FROM (SELECT id,x FROM src) src WHERE t.id=NEW.id AND src.id=t.id AND OLD.id=NEW.id", true},
		{"delete_using", "DELETE FROM t USING src WHERE t.id=NEW.id AND OLD.id=NEW.id AND t.a=(WITH v AS MATERIALIZED (SELECT src.x+t.id AS x) SELECT x FROM v)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			for _, sql := range []string{"CREATE TABLE input(id INT)", "CREATE TABLE t(id INT,spare INT,a INT,b INT)", "CREATE TABLE src(id INT,x INT)"} {
				sessionSQL(t, s, sql)
			}
			sessionSQL(t, s, "CREATE RULE original AS ON UPDATE TO input DO ALSO "+tc.action)
			before := e.CatalogRead()
			input := catalogRef(t, e, before, "input")
			persisted := rulesOf(before.MustRelation(input.Address))[0]
			action := persisted.actions[0]
			owner := action.presentation.Frames()[1]
			child := owner.Child
			columns := make(map[analyze.ViewLiteralPath]analyze.ViewColumn, len(action.presentation.Columns()))
			for key, column := range action.presentation.Columns() {
				columns[key] = column
			}
			// Names must come from current attributes, not stale raw identifiers.
			sessionSQL(t, s, "ALTER TABLE t DROP COLUMN spare")
			sessionSQL(t, s, "ALTER TABLE t RENAME COLUMN id TO renamed")
			text, err := ruleActionSQL(context.Background(), e.CatalogRead(), action)
			if err != nil {
				t.Fatal(err)
			}
			if owner.Child != child || action.presentation.Frames()[1] != owner || !reflect.DeepEqual(columns, action.presentation.Columns()) {
				t.Fatal("render mutated frozen namespace ownership")
			}
			syntax, err := parser.Parse(text)
			if err != nil || len(syntax) != 1 {
				t.Fatal("canonical action syntax", text, err)
			}
			if tc.grouped {
				update, ok := syntax[0].(*parser.Update)
				if !ok || len(update.Assignments) != 1 || len(update.Assignments[0].Targets) != 2 {
					t.Fatal("source was flattened or duplicated", syntax)
				}
				if _, ok := update.Assignments[0].Value.(parser.SubqueryExpr); !ok {
					t.Fatal("group lost independent SELECT", update.Assignments)
				}
			}
			sessionSQL(t, s, "CREATE RULE rebuilt AS ON UPDATE TO input DO ALSO "+text)
			current := e.CatalogRead()
			found := false
			for _, rule := range rulesOf(current.MustRelation(input.Address)) {
				if rule.Name != "rebuilt" {
					continue
				}
				found = true
				if tc.grouped {
					update := rule.actions[0].binding.Update
					if len(update.Groups) != 1 || update.Groups[0].Width != 2 || update.OutputWidth != 2 {
						t.Fatal("reanalysis lost tuple owner", update)
					}
				}
				if _, err := ruleActionSQL(context.Background(), current, rule.actions[0]); err != nil {
					t.Fatal("rebound source namespace", err)
				}
				mapped := rule.actions[0]
				expected, err := ruleActionSQL(context.Background(), current, mapped)
				if err != nil {
					t.Fatal(err)
				}
				for generation := range 3 {
					mapped.binding, err = remapRuleBinding(current, mapped.binding, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil)
					if err != nil {
						t.Fatal(err)
					}
					mapped.presentation, err = RemapView(current, mapped.presentation, nil, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil)
					if err != nil {
						t.Fatal(err)
					}
					var executable *plan.ProjectNode
					find := func(recipe expr.Expr) {
						plan.WalkExpression(recipe, func(node expr.Expr) {
							if query, ok := node.(plan.SubqueryExpr); ok && executable == nil {
								executable = query.QueryPlan.Root.(*plan.ProjectNode)
							}
						})
					}
					if mapped.binding.Update != nil {
						for _, recipe := range mapped.binding.Update.Assignments {
							find(recipe)
						}
					} else {
						find(mapped.binding.Predicate)
					}
					if executable == nil || mapped.presentation.Queries()[executable.Syntax] == executable {
						t.Fatal("fixture did not establish independently remapped executable and presentation graphs")
					}
					actual, err := ruleActionSQL(context.Background(), current, mapped)
					if err != nil || actual != expected {
						t.Fatal("canonical render after independent graph remap", generation, actual, expected, err)
					}
				}
			}
			if !found {
				t.Fatal("canonical action was not rebound")
			}
		})
	}
}
