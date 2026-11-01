package enginetests

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestNamedRelationPlanOwnership(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, descriptor := range sqltype.Builtins() {
		if descriptor.ID() == sqltype.Int4 {
			integer = descriptor
			break
		}
	}
	if integer == nil {
		t.Fatal("missing builtin integer descriptor")
	}
	schema := plan.QuerySchema{{Name: "id", Usage: integer.Usage(), Nullable: true}}
	scan := &plan.NamedRelationScanNode{QuerySchema: schema, Name: "x", RangeName: "a", ColumnAliases: []string{"k"}, PresenceSlot: 1}
	if left, right := plan.QueryInputs(scan); left != nil || right != nil {
		t.Fatal("named leaf acquired child ownership")
	}
	visited := 0
	plan.VisitExecutableQuery(scan, func(node plan.Node) {
		if node != scan {
			t.Fatal("unexpected child")
		}
		visited++
	}, func(expr.Expr) { t.Fatal("named leaf acquired expression state") })
	if visited != 1 {
		t.Fatal("named leaf missing from structural traversal", visited)
	}
	rewritten, err := plan.RewriteQueryExpressions(scan, func(value expr.Expr) (expr.Expr, bool, error) {
		t.Fatal("leaf attempted expression rewrite")
		return value, false, nil
	})
	if err != nil || rewritten != scan || &rewritten.Schema()[0] != &schema[0] {
		t.Fatal("identity rewrite changed immutable leaf backing", err)
	}
	var tree strings.Builder
	if err := plan.AppendQueryTree(context.Background(), &tree, scan); err != nil || !strings.Contains(tree.String(), `named_relation "x"`) {
		t.Fatal("structural presentation lost symbolic name", tree.String(), err)
	}
	if err := plan.AppendQueryTree(context.Background(), &tree, (*plan.NamedRelationScanNode)(nil)); err == nil {
		t.Fatal("typed nil named leaf accepted")
	}
	if estimate, err := plan.EstimateExplainNode(scan, nil, 0); err != nil || estimate.Rows != 0 {
		t.Fatal("empty named relation estimate lost", estimate, err)
	}
	// The only retained relation payload is ordinary immutable schema metadata.
	var columns []catalog.Column = scan.Schema()
	if len(columns) != 1 || scan.Name != "x" || scan.RangeName != "a" || scan.ColumnAliases[0] != "k" {
		t.Fatal(scan)
	}
}
