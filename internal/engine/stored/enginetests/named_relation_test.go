package enginetests_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestNamedRelationStoredRemap(t *testing.T) {
	read := engine.New().CatalogRead()
	int4, _ := read.LookupType(sqltype.Int4)
	int8, _ := read.LookupType(sqltype.Int8)
	schema := plan.QuerySchema{{Name: "v", Usage: int4.Usage(), Nullable: true}}
	leaf := &plan.NamedRelationScanNode{QuerySchema: schema, Name: "new_rows", RangeName: "n", ColumnAliases: []string{"alias"}, PresenceSlot: 4}
	mapper := func(typ *sqltype.Descriptor) *sqltype.Descriptor {
		if typ == int4 {
			return int8
		}
		return typ
	}
	mapped, err := stored.RemapViewQuery(read, leaf, func(ref ids.ObjectRef) ids.ObjectRef {
		t.Fatal("symbolic relation acquired catalog identity", ref)
		return ref
	}, mapper)
	if err != nil {
		t.Fatal(err)
	}
	next, ok := mapped.(*plan.NamedRelationScanNode)
	if !ok || next == leaf || next.Name != leaf.Name || next.RangeName != leaf.RangeName || next.PresenceSlot != leaf.PresenceSlot || next.ColumnAliases[0] != "alias" || next.Schema()[0].Usage.Type != int8 || next.Schema()[0].Name != "v" || !next.Schema()[0].Nullable {
		t.Fatal("symbolic leaf remap lost shape or aliases", mapped)
	}
	if leaf.Schema()[0].Usage.Type != int4 || &next.QuerySchema[0] == &leaf.QuerySchema[0] {
		t.Fatal("type remap mutated or retained input schema backing")
	}

	project := &plan.ProjectNode{QuerySchema: schema, Child: leaf, Expressions: []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: int4.Usage()}, Slot: 0}}}
	input := plan.SubqueryExpr{Info: expr.Info{Usage: int4.Usage()}, Kind: parser.SubqueryScalar, QueryPlan: &plan.Query{Root: project}}
	retagged, changed, err := stored.RetagTypes(read, input, mapper)
	if err != nil || !changed {
		t.Fatal("subquery retag", changed, err)
	}
	query := retagged.(plan.SubqueryExpr)
	retaggedLeaf := query.QueryPlan.Root.(*plan.ProjectNode).Child.(*plan.NamedRelationScanNode)
	if query.ResultUsage().Type != int8 || retaggedLeaf.Schema()[0].Usage.Type != int8 || retaggedLeaf.Name != leaf.Name || retaggedLeaf.RangeName != leaf.RangeName || retaggedLeaf.PresenceSlot != leaf.PresenceSlot || leaf.Schema()[0].Usage.Type != int4 {
		t.Fatal("subquery retag lost symbolic relation or changed source")
	}
}

func TestNamedRelationStoredPersistence(t *testing.T) {
	read := engine.New().CatalogRead()
	int4, _ := read.LookupType(sqltype.Int4)
	leaf := &plan.NamedRelationScanNode{Name: "new_rows", QuerySchema: plan.QuerySchema{{Name: "v", Usage: int4.Usage()}}}
	for name, root := range map[string]plan.Node{
		"direct":     leaf,
		"derived":    &plan.DerivedNode{Query: &plan.Query{Root: leaf}},
		"subquery":   &plan.ProjectNode{Child: &plan.SingletonNode{}, Expressions: []expr.Expr{plan.SubqueryExpr{QueryPlan: &plan.Query{Root: leaf}}}},
		"unused CTE": &plan.ProjectNode{Child: &plan.SingletonNode{}, AnalysisExpressions: []expr.Expr{plan.SubqueryExpr{CTEEdge: true, QueryPlan: &plan.Query{Root: leaf}}}},
	} {
		t.Run(name, func(t *testing.T) {
			binding := analyze.Binding{Query: &plan.Query{Root: root}}
			definition, err := stored.FreezeQueryPresentation(read, ids.SessionToken{}, &parser.Select{}, binding, false, false, 1, "", false)
			var got *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: `transition table "new_rows" cannot be referenced in a persistent object`}
			if definition != nil || !errors.As(err, &got) || *got != want {
				t.Fatalf("persistent invocation relation captured: %v %#v %v", definition, got, err)
			}
		})
	}
	// An ordinary catalog-free query still freezes; named leaves are not replaced
	// with invented sources, empty relations, or a serializer fallback.
	binding := analyze.Binding{Query: &plan.Query{Root: &plan.SingletonNode{}}}
	if definition, err := stored.FreezeQueryPresentation(read, ids.SessionToken{}, &parser.Select{}, binding, false, false, 1, "", false); err != nil || definition == nil {
		t.Fatal("ordinary freeze control", definition, err)
	}
}
