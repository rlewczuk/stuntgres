package engine

import (
	"context"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestArrayAggregateViewRemap(t *testing.T) {
	for _, arrays := range []bool{false, true} {
		p := arrayAggregateUnitProvider(t, arrays)
		element := *p.usage.Type.Element()
		array, err := sqltype.Array(p.usage.Type.ID(), p.usage.Type.Name(), &element)
		if err != nil {
			t.Fatal(err)
		}
		root := &aggregateNode{
			querySchema: querySchema{{Name: "array_agg", Usage: p.usage, Nullable: true, NullabilityKnown: true}},
			child:       &valuesNode{},
			calls:       []aggregateCall{{provider: p, includeNulls: true}},
		}
		mapped, err := remapViewQuery(root, func(ref ObjectRef) ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			if old == p.usage.Type {
				return array
			}
			if old == p.usage.Type.Element() {
				return &element
			}
			return old
		})
		if err != nil {
			t.Fatal(err)
		}
		call := mapped.(*aggregateNode).calls[0]
		provider := call.provider.(*arrayAggregateProvider)
		argument := &element
		if arrays {
			argument = array
		}
		if provider == p || provider.argument != argument || provider.usage.Type != array || provider.arrays != arrays || !call.includeNulls || mapped.Schema()[0].Usage.Type != array {
			t.Fatal("view provider/result remint", provider, mapped.Schema())
		}
		if p.usage.Type == array || p.argument == argument {
			t.Fatal("remap mutated retained source provider")
		}
		state, err := provider.Init(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Finalize(context.Background(), state); err == nil {
			t.Fatal("reminted state accepted by source provider")
		}
	}
}
