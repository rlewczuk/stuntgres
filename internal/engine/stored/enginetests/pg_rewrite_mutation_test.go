package enginetests_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestPGRewriteMutationSemantics(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	for _, sql := range []string{"CREATE TABLE mut_event(v integer)", "CREATE TABLE mut_input(v integer)", "CREATE TABLE mut_target(v integer)", "CREATE TABLE mut_sink(v integer)", "CREATE SCHEMA mut_a", "CREATE SCHEMA mut_b", "CREATE AGGREGATE mut_a.pick(integer)(SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')", "CREATE AGGREGATE mut_b.pick(integer)(SFUNC=pg_catalog.int4larger,STYPE=integer,INITCOND='0')"} {
		enginetest.SessionSQL(t, session, sql)
	}
	definition := "CREATE OR REPLACE RULE r AS ON INSERT TO public.mut_event DO ALSO WITH moved AS (INSERT INTO public.mut_target SELECT (SELECT pick(v) FROM public.mut_input) RETURNING v) INSERT INTO public.mut_sink SELECT v FROM moved"
	var original sqltype.Value
	var first string
	var selected [2]ids.ObjectAddress
	for i, schema := range []string{"mut_a", "mut_b"} {
		enginetest.SessionSQL(t, session, "SET search_path TO "+schema+",public,pg_catalog")
		enginetest.SessionSQL(t, session, definition)
		read := e.CatalogRead()
		owner := read.MustRelation(enginetest.CatalogRef(t, e, read, "mut_event").Address)
		list := owner.Rules.(stored.RuleList)
		if len(list) != 1 {
			t.Fatal(list)
		}
		parts := list[0].Parts()
		if len(parts.Actions) != 1 {
			t.Fatal(parts)
		}
		binding := parts.Actions[0].Parts().Binding
		modifiers := 0
		if err := stored.VisitRuleModifiers(binding, func(definition *plan.CTEDefinition, mutation *analyze.Binding) error {
			modifiers++
			if definition.Statement == nil || mutation.Relation == nil || mutation.Returning == nil || mutation.Query == nil || len(mutation.Targets) != 1 {
				t.Fatal("missing genuine mutation owner facts", definition, mutation)
			}
			var visitQuery func(plan.Node)
			visitQuery = func(node plan.Node) {
				if node == nil {
					return
				}
				if stage, ok := node.(*plan.AggregateNode); ok {
					if len(stage.Calls) != 1 {
						t.Fatal(stage.Calls)
					}
					selected[i] = stage.Calls[0].Routine.Address
				}
				plan.QueryExpressions(node, func(expression expr.Expr) {
					plan.WalkExpression(expression, func(child expr.Expr) {
						if subquery, ok := child.(plan.SubqueryExpr); ok && subquery.QueryPlan != nil {
							visitQuery(subquery.QueryPlan.Root)
						}
					})
				})
				left, right := plan.QueryInputs(node)
				visitQuery(left)
				visitQuery(right)
			}
			visitQuery(mutation.Query.Root)
			if selected[i].ObjectID == 0 {
				t.Fatal("genuine mutation subquery lost selected aggregate")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if modifiers != 1 {
			t.Fatal("modifier owner lost", modifiers)
		}
		typ, _ := read.LookupType(sqltype.PgNodeTree)
		_, value, err := stored.RewriteTrees(t.Context(), read, owner, typ, &list[0], nil)
		if err != nil {
			t.Fatal(err)
		}
		text, err := value.Type().Output(t.Context(), value)
		if err != nil {
			t.Fatal(err)
		}
		for _, fragment := range []string{"modifier owner", "attribute 1", "Returning", "SubLink", fmt.Sprintf("%d/%d/0", selected[i].ClassID, selected[i].ObjectID)} {
			if !strings.Contains(text, fragment) {
				t.Fatal("missing retained modifying owner fact", fragment, text)
			}
		}
		if i == 0 {
			original, first = value, text
		} else if first == text || selected[0] == selected[1] {
			t.Fatal("replacement collapsed selected modifying-CTE ownership", selected)
		}
	}
	enginetest.SessionSQL(t, session, "DROP RULE r ON public.mut_event")
	enginetest.SessionSQL(t, session, "DROP AGGREGATE mut_a.pick(integer)")
	tree, _ := original.Tree()
	if text, err := tree.StructuralOutput(t.Context()); err != nil || text != first {
		t.Fatal("modifier predecessor lost original owned tree", text, err)
	}
}
