package enginetests_test

import (
	"context"
	"maps"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestXMLXPathFrozenOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE xpath_owner(p text,x xml,m text[])")
	enginetest.SessionSQL(t, s, "CREATE VIEW xpath_owned AS SELECT xpath(p,x) AS a,xpath(p,x,m) AS b,xpath_exists(p,x) AS c,xpath_exists(p,x,m) AS d FROM xpath_owner")
	read := e.CatalogRead()
	relation := preparedFoldStoredRelation(t, e, "xpath_owned")
	accepted := stored.ViewOf(relation)
	root := accepted.Query().Root.(*plan.ProjectNode)
	syntax := accepted.Parts().Syntax
	if err := accepted.Validate(read, relation); err != nil {
		t.Fatal(err)
	}
	refs := maps.Clone(accepted.Parts().RoutineRefs)
	for index, wantOID := range []uint32{2932, 2931, 3050, 3049} {
		node, ok := root.Expressions[index].(functions.XMLXPath)
		if !ok || node.CallRoutineReference() == nil || node.CallRoutineReference().RoutineOID() != wantOID {
			t.Fatal("selected overload", index, root.Expressions[index])
		}
		arity := 2 + index%2
		if len(node.Children()) != arity {
			t.Fatal("written arity")
		}
		backing := &node.Args[0]
		generation := node.ResultUsage().Type
		capture, ok, err := node.Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Count != arity {
			t.Fatal(capture, err)
		}
		for i := range arity {
			child, err := capture.At(node, i)
			if err != nil || child != node.Args[i] {
				t.Fatal("capture order", err)
			}
		}
		rendered, err := node.Render()
		if err != nil || len(rendered.Args) != arity || rendered.Name != node.Name {
			t.Fatal(rendered, err)
		}
		rebuilt, err := node.Rebuild(expr.InfoOf(node), node.Children())
		if err != nil || rebuilt.ResultUsage().Type != generation || &rebuilt.(functions.XMLXPath).Args[0] != backing {
			t.Fatal("rebuild ownership", err)
		}
		retagged, changed, err := stored.RetagTypes(read, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil || changed || !reflect.DeepEqual(retagged, node) {
			t.Fatal("identity retag", changed, err)
		}
		remapped, err := stored.RemapExpression(node, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil)
		if err != nil || !reflect.DeepEqual(remapped, node) {
			t.Fatal("identity remap", err)
		}
		foreign := *generation
		info := expr.InfoOf(node)
		info.Usage.Type = &foreign
		if _, err := node.Rebuild(info, node.Children()); err == nil {
			t.Fatal("foreign result generation accepted")
		}
		if _, err := node.Rebuild(expr.InfoOf(node), node.Children()[:1]); err == nil {
			t.Fatal("wrong arity accepted")
		}
		bad := node
		bad.RoutineReference = root.Expressions[(index+1)%4].(functions.XMLXPath).RoutineReference
		if bad.ValidateStoredProvider() == nil {
			t.Fatal("substituted overload accepted")
		}
		original := syntax.Targets[index].(parser.FunctionExpr)
		rejected := accepted.With(func(parts *stored.ViewParts) {
			next := *syntax
			next.Targets = append([]parser.Expr(nil), syntax.Targets...)
			raw := original
			raw.Args = append([]parser.Expr(nil), raw.Args...)
			raw.Args[0] = parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "/forged", Pos: raw.Args[0].SourcePosition()}}
			next.Targets[index] = raw
			project := *root
			project.Syntax = &next
			query := *parts.Query
			query.Root = &project
			parts.Query, parts.Syntax = &query, &next
			parts.Frames = maps.Clone(parts.Frames)
			parts.Frames[project.Frame] = &project
			parts.Queries = maps.Clone(parts.Queries)
			delete(parts.Queries, syntax)
			parts.Queries[&next] = &project
		})
		if err := rejected.ValidateLiterals(read); err == nil {
			t.Fatal("forged source accepted")
		}
		if &node.Args[0] != backing || node.ResultUsage().Type != generation || !reflect.DeepEqual(original, syntax.Targets[index]) {
			t.Fatal("rejected publication mutated accepted provider")
		}
	}
	if accepted.Query().Root != root || accepted.Parts().Syntax != syntax || !reflect.DeepEqual(refs, accepted.Parts().RoutineRefs) {
		t.Fatal("accepted backing changed")
	}
	for _, deparse := range []bool{false, true} {
		if _, err := stored.ViewDefinitionSQL(context.Background(), read, relation, stored.Presentation{Deparse: deparse}); err != nil {
			t.Fatal(err)
		}
	}
}
