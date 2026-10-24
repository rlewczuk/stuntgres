package index

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestDroppedUniqueOIDsFollowActionOrder(t *testing.T) {
	old := &catalog.Relation{}
	old.Constraints.Unique = []catalog.UniqueConstraint{{Name: "a", OID: 10}, {Name: "b", OID: 20}}
	drop := func(name string) parser.AlterTableAction {
		return parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: name}}
	}
	actions := []parser.AlterTableAction{drop("b"), {Action: parser.AddNotNull, Constraint: parser.Identifier{Text: "a"}}, drop("check_c"), drop("a")}
	if got := DroppedUniqueOIDs(old, actions); !slices.Equal(got, []ids.OID{20, 10}) {
		t.Fatalf("dropped unique OIDs = %v, want [20 10]", got)
	}
}
