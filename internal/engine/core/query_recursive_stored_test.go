package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func TestQueryRecursiveCTEStoredNamespaceIdentity(t *testing.T) {
	typ := cteUnitType(t)
	table := &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "r"}}}}
	definition := &plan.CTEDefinition{Recursive: true}
	self := &plan.WorktableScanNode{DerivedNode: plan.DerivedNode{QuerySchema: plan.QuerySchema{{Name: "n", Usage: typ.Usage()}}, Table: table, CTE: definition, Ordinal: -1}}
	project := &plan.ProjectNode{Child: &plan.FilterNode{Child: self}}
	if stored.ViewCTETable(project, table) != self || stored.ViewCTEOrdinal(project, -1) != self {
		t.Fatal("stored self scan lost opaque node identity")
	}
	renderer := &stored.QueryRenderer{Slots: map[plan.Node]map[int]string{self: {0: "r.n"}}}
	text, err := renderer.SlotSQL(context.Background(), catalog.Read{}, stored.ViewCTETable(project, table), 0)
	if err != nil || text != "r.n" {
		t.Fatal("worktable namespace slot", text, err)
	}
}
