package enginetests

import (
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

func TestPgStatStatementsFingerprintJoinProvenance(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE pgss_comma_a(k integer)")
	enginetest.Run(t, e, "CREATE TABLE pgss_comma_b(k integer)")
	comma := pgssAnalyzedJumble(t, e, "SELECT a.k FROM pgss_comma_a a,pgss_comma_b b")
	cross := pgssAnalyzedJumble(t, e, "SELECT a.k FROM pgss_comma_a a CROSS JOIN pgss_comma_b b")
	if comma.QueryID() == cross.QueryID() {
		t.Fatal("FROM list collapsed to explicit JoinExpr")
	}
	_, binding := pgssAnalyzedBinding(t, e, "SELECT a.k FROM pgss_comma_a a,pgss_comma_b b")
	var find func(plan.Node) *plan.JoinNode
	find = func(root plan.Node) *plan.JoinNode {
		if root == nil {
			return nil
		}
		if join, ok := root.(*plan.JoinNode); ok {
			return join
		}
		left, right := plan.QueryInputs(root)
		if join := find(left); join != nil {
			return join
		}
		return find(right)
	}
	original := find(binding.Query.Root)
	if original == nil || !original.Comma {
		t.Fatal("analysis lost FROM-list provenance")
	}
	left := original.Left
	rewritten, err := plan.RewriteQueryExpressions(original, func(node expr.Expr) (expr.Expr, bool, error) { return node, false, nil }, func(node plan.Node) (plan.Node, error) {
		if source, ok := node.(*plan.SourceNode); ok {
			copy := *source
			return &copy, nil
		}
		return node, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := rewritten.(*plan.JoinNode)
	if rebuilt == original || !rebuilt.Comma || rebuilt.Left == left || original.Left != left {
		t.Fatal("query rewrite lost provenance or changed accepted source")
	}
	remapped, err := stored.RemapViewQuery(e.CatalogRead(), original, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
	if err != nil {
		t.Fatal(err)
	}
	mapped := remapped.(*plan.JoinNode)
	if mapped == original || !mapped.Comma || original.Left != left {
		t.Fatal("stored remap lost provenance or changed accepted source")
	}
	// The raw utility projection likewise preserves selected FROM structure.
	rawComma, _ := analyze.JumbleUtility(enginetest.Statement(t, "CREATE VIEW pgss_comma_view AS SELECT a.k FROM pgss_comma_a a,pgss_comma_b b"))
	rawCross, _ := analyze.JumbleUtility(enginetest.Statement(t, "CREATE VIEW pgss_comma_view AS SELECT a.k FROM pgss_comma_a a CROSS JOIN pgss_comma_b b"))
	if rawComma.QueryID() == rawCross.QueryID() {
		t.Fatal("raw utility lost selected FROM layout")
	}
}
