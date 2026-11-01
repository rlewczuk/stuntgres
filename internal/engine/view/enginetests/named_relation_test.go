package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

func TestNamedRelationPreflightOwnership(t *testing.T) {
	read := engine.New().CatalogRead()
	typ, ok := read.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing source-owned column type")
	}
	leaf := &plan.NamedRelationScanNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: typ.Usage()}}, Name: "new_rows", RangeName: "n", ColumnAliases: []string{"value"}, PresenceSlot: 2}
	for _, static := range []bool{false, true} {
		c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), PlanStaticOperators: static}}
		next, err := view.PreflightQuery(c, leaf)
		if err != nil || next != leaf {
			t.Fatal("preflight must retain symbolic leaf identity", next, err)
		}
		if leaf.Name != "new_rows" || leaf.RangeName != "n" || leaf.ColumnAliases[0] != "value" || leaf.Schema()[0].Name != "v" || leaf.PresenceSlot != 2 {
			t.Fatal("preflight changed immutable shape or alias recipe")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
	if next, err := view.PreflightQuery(c, leaf); next != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("symbolic leaf bypassed ordinary cancellation", next, err)
	}
}
