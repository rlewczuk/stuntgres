package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"testing"
)

func TestPgStatStatementsFingerprintSystemAttributes(t *testing.T) {
	binder, namespace := tableOIDBinder(t)
	fingerprint := func(n plan.SystemAttributeExpr, label string) StatementJumble {
		t.Helper()
		root := &plan.ProjectNode{
			Frame:       4,
			QuerySchema: plan.QuerySchema{{Name: label, Usage: n.ResultUsage()}},
			Expressions: []expr.Expr{n},
			Child:       &plan.SourceNode{Ref: n.Owner, RangeName: "r", Occurrence: 0},
		}
		binding := Binding{Query: &plan.Query{Root: root}}
		return JumbleStatement(&parser.Select{}, &binding)
	}
	seen := make(map[int64]string)
	var xmin plan.SystemAttributeExpr
	for _, name := range []string{"ctid", "xmin", "xmax", "tableoid"} {
		node, err := binder.querySystemAttribute(name, namespace, &namespace.Qualifiers[0], nil, 4, parser.Position{Offset: 7, Character: 8})
		if err != nil {
			t.Fatal(err)
		}
		n := node.(plan.SystemAttributeExpr)
		before := fingerprint(n, name)
		if previous, exists := seen[before.QueryID()]; exists || before.QueryID() == 0 {
			t.Fatal("distinct system subIDs merged, including the two XID attributes", previous, name)
		}
		seen[before.QueryID()] = name
		if name == "xmin" {
			xmin = n
		}
		// The binder's stable Var coordinates are selected, not catalog lifetime,
		// physical identity, source positions, or output aliases.
		copy := n
		address := copy.Owner.Address
		address.ObjectID++
		copy.Owner = new(ids.Domain).Ref(copy.Owner.Epoch+1, address)
		copy.Info.Pos = parser.Position{Offset: 19, Character: 20}
		after := fingerprint(copy, "renamed_output")
		if before.QueryID() != after.QueryID() || len(after.Locations) != 0 || len(after.Lists) != 0 {
			t.Fatal("ignored system Var metadata changed grouping or constant normalization", name, before, after)
		}
		occurrence := n
		occurrence.QueryOccurrence++
		if fingerprint(occurrence, name).QueryID() == before.QueryID() {
			t.Fatal("source occurrence was omitted", name)
		}
		prior := n
		prior.Old = true
		if fingerprint(prior, name).QueryID() == before.QueryID() {
			t.Fatal("RETURNING OLD identity was omitted", name)
		}
		side := n
		side.Side = plan.MergeReturningSide(1)
		if fingerprint(side, name).QueryID() == before.QueryID() {
			t.Fatal("RETURNING side identity was omitted", name)
		}
	}
	// Lexical depth, not the absolute frame allocator, selects a correlated Var.
	nested := func(n plan.SystemAttributeExpr, outerFrame, innerFrame uint32) int64 {
		inner := &plan.ProjectNode{Frame: innerFrame, Expressions: []expr.Expr{n}, Child: &plan.SingletonNode{}}
		edge := plan.SubqueryExpr{Info: n.Info, QueryPlan: &plan.Query{Root: inner}}
		outer := &plan.ProjectNode{Frame: outerFrame, Expressions: []expr.Expr{edge}, Child: &plan.SingletonNode{}}
		binding := Binding{Query: &plan.Query{Root: outer}}
		return JumbleStatement(&parser.Select{}, &binding).QueryID()
	}
	outer := nested(xmin, 4, 8)
	local := xmin
	local.Frame = 8
	if outer == nested(local, 4, 8) {
		t.Fatal("correlated and local system attributes merged")
	}
	rebased := xmin
	rebased.Frame = 40
	if outer != nested(rebased, 40, 80) {
		t.Fatal("absolute frame numbering changed lexical-depth identity")
	}
}
