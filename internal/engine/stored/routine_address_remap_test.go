package stored

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// OriginTable is an address-only metadata projection. SourceNode.Ref is an
// actual lifetime capture. Mapping the former must not manufacture authority
// for a foreign instance of the latter, even when both contain the same OID.
func TestRoutineAddressProjectionRemapBoundary(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		name := "projected-origin"
		if foreign {
			name = "foreign-retained-source"
		}
		t.Run(name, func(t *testing.T) {
			read, original, _ := routineMergeCaptureFixture(t)
			action := original.Action(0)
			source := *action.binding.Relation
			source.ObjectAddress.ObjectID = 904
			source.ObjectName = "source"
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			source.Columns[0].OriginTable = uint32(source.Address().ObjectID)
			source.Columns[0].OriginAttribute = 1
			read.State().SetDescriptor(source.Address(), &source)
			ref := read.Owner().Domain.Ref(read.Epoch(), source.Address())
			leaf := &plan.SourceNode{QuerySchema: source.Columns, Ref: ref, Occurrence: 0}
			action.binding.QuerySources = []exec.Leaf{{Ref: ref, Relation: &source, Uses: []int{0}}}
			action.binding.Merge.Source = leaf
			action.binding.Merge.SourceWidth = 1
			action.binding.Merge.TargetWidth = len(action.binding.Relation.Columns)
			project := action.presentation.query.Root.(*plan.ProjectNode)
			target := project.Child
			project.Child = &plan.JoinNode{QuerySchema: append(append(plan.QuerySchema(nil), target.Schema()...), leaf.Schema()...), Left: target, Right: leaf, LeftWidth: len(target.Schema()), RightWidth: len(leaf.Schema())}
			action.presentation.sources = []ViewLeaf{{Ref: ref, Attributes: source.Attributes, Reads: []int32{1}}}
			program := NewRoutineProgram([]RuleAction{action})
			if err := program.Validate(read); err != nil {
				t.Fatal("valid source/origin control", err)
			}
			if foreign {
				leaf.Ref = new(ids.Domain).Ref(read.Epoch(), source.Address())
			}
			replacement := source
			replacement.ObjectAddress.ObjectID = 905
			replacement.Columns = append([]catalog.Column(nil), source.Columns...)
			replacement.Columns[0].OriginTable = uint32(replacement.Address().ObjectID)
			read.State().SetDescriptor(replacement.Address(), &replacement)
			next, err := program.Rebind(read, map[ids.ObjectAddress]ids.ObjectAddress{source.Address(): replacement.Address()})
			if foreign {
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatalf("foreign retained source acquired local authority: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal("address-only origin rejected as a lifetime capture", err)
			}
			binding := next.(*RoutineProgram).Action(0).Binding()
			mapped := binding.Merge.Source.(*plan.SourceNode)
			if mapped.Ref != read.Owner().Domain.Ref(read.Epoch(), replacement.Address()) || mapped.Schema()[0].OriginTable != uint32(replacement.Address().ObjectID) {
				t.Fatal("retained identity and metadata origin did not map together", mapped.Ref, mapped.Schema())
			}
			if leaf.Ref != ref || leaf.Schema()[0].OriginTable != uint32(source.Address().ObjectID) {
				t.Fatal("replacement mutated original source capture or origin")
			}
		})
	}
}
