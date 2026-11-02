package stored

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"testing"
)

func TestStoredSystemAttributeCapture(t *testing.T) {
	for _, name := range []string{"ctid", "xmin", "xmax", "tableoid"} {
		t.Run(name, func(t *testing.T) {
			read, program, path := storedSystemAttributeFixture(t, name)
			if err := program.Validate(read); err != nil {
				t.Fatal("valid typed capture rejected", err)
			}
			action := program.actions[0]
			n := action.binding.Query.Root.(*plan.ProjectNode).Expressions[0].(plan.SystemAttributeExpr)
			refs, err := ExpressionDependRefs(context.Background(), read, n)
			if err != nil || len(refs) != 1 || refs[0].SubID != n.SubID || refs[0].ObjectID != n.Owner.Address.ObjectID {
				t.Fatal("lost attribute identity", refs, err)
			}
			renderer := QueryRenderer{View: action.presentation}
			source := action.binding.Query.Root.(*plan.ProjectNode).Child
			renderer.setSourceQualifier(source, "physical_alias")
			text, err := renderer.systemAttributeSQL(read, action.presentation.columns[path])
			if err != nil || text != "physical_alias."+name {
				t.Fatal("rendered wrong system attribute", text, err)
			}
			// A negative subID is part of capture identity, not merely its XID type.
			column := action.presentation.columns[path]
			if column.SystemAttribute == -2 {
				column.SystemAttribute = -4
			} else {
				column.SystemAttribute = -2
			}
			action.presentation.columns[path] = column
			if err := program.Validate(read); err == nil {
				t.Fatal("substituted attribute capture published")
			}
		})
	}
}

func TestStoredSystemAttributeRemap(t *testing.T) {
	for _, name := range []string{"ctid", "xmin", "xmax"} {
		t.Run(name, func(t *testing.T) {
			read, program, path := storedSystemAttributeFixture(t, name)
			original := program.actions[0].binding.Query.Root.(*plan.ProjectNode).Expressions[0].(plan.SystemAttributeExpr)
			oldAddress := original.Owner.Address
			newAddress := oldAddress
			newAddress.ObjectID++
			destination := *read.MustRelation(oldAddress)
			destination.ObjectAddress = newAddress
			read.State().SetDescriptor(newAddress, &destination)
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{oldAddress: newAddress}
			next, err := program.Rebind(read, mapping, NewRoutineRemap(read, mapping, nil, nil, false))
			if err != nil {
				t.Fatal(err)
			}
			mapped := next.(*RoutineProgram).actions[0]
			n := mapped.binding.Query.Root.(*plan.ProjectNode).Expressions[0].(plan.SystemAttributeExpr)
			capture := mapped.presentation.columns[path]
			if n.Owner.Address != newAddress || n.SubID != original.SubID || capture.SystemAttribute != original.SubID || capture.Owner != n.Owner || n.Frame != original.Frame || n.QueryOccurrence != original.QueryOccurrence {
				t.Fatal("remap changed attribute or frame identity", n, capture)
			}
			if err := next.Validate(read); err != nil {
				t.Fatal("remapped typed capture invalid", err)
			}
			if before := program.actions[0].binding.Query.Root.(*plan.ProjectNode).Expressions[0].(plan.SystemAttributeExpr); before.Owner != original.Owner || program.actions[0].presentation.columns[path].Owner != original.Owner {
				t.Fatal("remap mutated retained source")
			}
			mapper := NewRuleTreeMapper(read, func(frame uint32) uint32 { return frame + 3 }, 5, nil)
			mappedExpr, _, err := mapper.Expression(original)
			if err != nil {
				t.Fatal(err)
			}
			rebased := mappedExpr.(plan.SystemAttributeExpr)
			if rebased.SubID != original.SubID || rebased.Frame != 4 || rebased.QueryOccurrence != 6 || rebased.Owner != original.Owner {
				t.Fatal("clone/walk coordinate remap lost attribute", rebased)
			}
		})
	}
}
