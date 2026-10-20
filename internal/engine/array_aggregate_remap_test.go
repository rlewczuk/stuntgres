package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func arrayAggregateUnitProvider(t *testing.T, arrays bool) *functions.ArrayAggregateProvider {
	t.Helper()
	e := New()
	element, ok := e.CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("integer type")
	}
	array, ok := e.CatalogRead().LookupArray(element)
	if !ok {
		t.Fatal("integer array")
	}
	arg := element
	if arrays {
		arg = array
	}
	return functions.NewArrayAggregateProvider(arg, array.Usage(), arrays)
}
func TestArrayAggregateViewRemap(t *testing.T) {
	e := New()
	for _, arrays := range []bool{false, true} {
		p := arrayAggregateUnitProvider(t, arrays)
		element := *p.ResultUsage().Type.Element()
		array, err := sqltype.Array(p.ResultUsage().Type.ID(), p.ResultUsage().Type.Name(), &element)
		if err != nil {
			t.Fatal(err)
		}
		root := &aggregateNode{
			QuerySchema: querySchema{{Name: "array_agg", Usage: p.ResultUsage(), Nullable: true, NullabilityKnown: true}},
			Child:       &valuesNode{},
			Calls:       []aggregateCall{{Provider: p, IncludeNulls: true}},
		}
		mapped, err := remapViewQuery(e.catalog.read(e.catalog.state), root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			if old == p.ResultUsage().Type {
				return array
			}
			if old == p.ResultUsage().Type.Element() {
				return &element
			}
			return old
		})
		if err != nil {
			t.Fatal(err)
		}
		call := mapped.(*aggregateNode).Calls[0]
		provider := call.Provider.(*functions.ArrayAggregateProvider)
		argument := &element
		if arrays {
			argument = array
		}
		if provider == p || provider.ArgumentType() != argument || provider.ResultUsage().Type != array || provider.Arrays() != arrays || !call.IncludeNulls || mapped.Schema()[0].Usage.Type != array {
			t.Fatal("view provider/result remint", provider, mapped.Schema())
		}
		if p.ResultUsage().Type == array || p.ArgumentType() == argument {
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

func TestArrayAggregateCollationViewRemap(t *testing.T) {
	source, destination := New(), New()
	sourceSession := sessionStart(t, source, nil)
	destinationSession := sessionStart(t, destination, nil)
	sessionSQL(t, sourceSession, `CREATE COLLATION aggregate_source_full FROM pg_catalog.pg_unicode_fast`)
	sessionSQL(t, destinationSession, `CREATE COLLATION aggregate_destination_anchor FROM pg_catalog."C"`)
	sessionSQL(t, destinationSession, `CREATE COLLATION aggregate_destination_ascii FROM pg_catalog."C"`)
	sourceView, destinationView := source.CatalogRead(), destination.CatalogRead()
	capability := func(view catalog.Read, name string) sqltype.CollationRef {
		t.Helper()
		address := view.NameAddress(ids.NameKey{ClassID: ids.CollationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
		provider, ok := view.Collation(address)
		if !ok {
			t.Fatal("missing genuine registered collation", name)
		}
		result, err := provider.Usage()
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	sourceCapability := capability(sourceView, "aggregate_source_full")
	destinationCapability := capability(destinationView, "aggregate_destination_ascii")
	if !sourceCapability.Valid() || !destinationCapability.Valid() || sourceCapability.ID == destinationCapability.ID || !sourceCapability.Unicode() || destinationCapability.Unicode() {
		t.Fatal("fixture requires distinct genuine Full/ASCII capabilities", sourceCapability, destinationCapability)
	}
	sourceElement, ok := sourceView.LookupType(sqltype.Text)
	if !ok {
		t.Fatal("source character type")
	}
	sourceArray, ok := sourceView.LookupArray(sourceElement)
	if !ok {
		t.Fatal("source character array")
	}
	destinationElement, ok := destinationView.LookupType(sqltype.Text)
	if !ok {
		t.Fatal("destination character type")
	}
	destinationArray, ok := destinationView.LookupArray(destinationElement)
	if !ok {
		t.Fatal("destination character array")
	}
	for _, arrays := range []bool{false, true} {
		argument := sourceElement
		expectedArgument := destinationElement
		if arrays {
			argument, expectedArgument = sourceArray, destinationArray
		}
		sourceUsage := sourceArray.Usage()
		sourceUsage.Collation = sourceCapability
		p := functions.NewArrayAggregateProvider(argument, sourceUsage, arrays)
		root := &aggregateNode{
			QuerySchema: querySchema{{Name: "array_agg", Usage: sourceUsage, Nullable: true, NullabilityKnown: true}},
			Child:       &valuesNode{},
			Calls:       []aggregateCall{{Provider: p, IncludeNulls: true}},
		}
		mapped, err := remapViewQuery(destinationView, root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			if old == sourceElement {
				return destinationElement
			}
			if old == sourceArray {
				return destinationArray
			}
			return old
		}, func(old sqltype.CollationRef) (sqltype.CollationRef, error) {
			if old == sourceCapability {
				return destinationCapability, nil
			}
			return old, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		call := mapped.(*aggregateNode).Calls[0]
		provider := call.Provider.(*functions.ArrayAggregateProvider)
		want := destinationArray.Usage()
		want.Collation = destinationCapability
		if provider == p || provider.ArgumentType() != expectedArgument || provider.Arrays() != arrays || !call.IncludeNulls || mapped.Schema()[0].Usage != want || provider.ResultUsage() != want || provider.ResultUsage() != mapped.Schema()[0].Usage {
			t.Fatal("complete remapped array aggregate Usage", arrays, provider.ResultUsage(), mapped.Schema()[0].Usage, want)
		}
		if !provider.ResultUsage().Collation.Valid() || provider.ResultUsage().Collation.CaseMode() != destinationCapability.CaseMode() {
			t.Fatal("destination provider capability is not actual registered ASCII", provider.ResultUsage())
		}
		if p.ResultUsage() != sourceUsage || p.ArgumentType() != argument || root.Schema()[0].Usage != sourceUsage || p.ResultUsage().Collation.CaseMode() != sourceCapability.CaseMode() {
			t.Fatal("remap mutated retained Full source authority", p.ResultUsage(), sourceUsage)
		}
		state, err := provider.Init(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Finalize(context.Background(), state); err == nil {
			t.Fatal("remapped aggregate state accepted by source provider")
		}
	}
}
