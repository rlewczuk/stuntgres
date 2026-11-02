package schemacmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func TestIntarrayProviderRemap(t *testing.T) {
	source, original, _ := intarrayUnitInstalled(t)
	sourceView := source.CatalogRead()
	target := engine.New()
	owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
	defer closeOwner()
	namespace := intarrayUnitTemporaryNamespace(t, target, owner)
	extensionUnitPublish(t, target, owner, intarrayUnitStage(t, target, owner, "unaccent", namespace.Name()))
	if _, err := target.ReplaceCatalog(context.Background(), source, sourceView); err != nil {
		t.Fatal(err)
	}
	view := target.CatalogRead()
	installed := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"}))
	if err := extension.StockIntarray.ValidateExtension(view, installed); err != nil {
		t.Fatal(err)
	}
	imports := make(map[*sqltype.Descriptor]*sqltype.Descriptor)
	for _, slot := range []int{0, 3} {
		before := sourceView.MustType(original.Installation.Members[slot])
		after := view.MustType(installed.Installation.Members[slot])
		if before.SQLType == after.SQLType || before.SQLType.ID() == after.SQLType.ID() || after.Owner != installed.Owner || after.Namespace() != installed.TargetNamespace || after.Origin.Installation != installed.Installation || after.Origin.Declaration != slot {
			t.Fatal("displaced destination type role", before, after)
		}
		imports[before.SQLType] = after.SQLType
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(after.Array)})
		if array.SQLType.Element() != after.SQLType || array.Owner != after.Owner || array.Namespace() != after.Namespace() {
			t.Fatal("companion retained source identity", array)
		}
		input := view.MustProcedure(installed.Installation.Members[slot+1])
		output := view.MustProcedure(installed.Installation.Members[slot+2])
		if input.Result != after.Address().ObjectID || input.Signature != "2275" || output.Result != 2275 || output.Signature != extension.QueryIntSignature(after.SQLType.ID()) {
			t.Fatal("I/O remap lost dynamic result/formal", input, output)
		}
		for _, proc := range []*catalog.Procedure{input, output} {
			if _, err := extension.ProcedureSymbol(view, proc); err != nil {
				t.Fatal("destination sealed I/O authority", err)
			}
		}
	}
	before := sourceView.MustType(original.Installation.Members[3])
	after := view.MustType(installed.Installation.Members[3])
	null := sqltype.Null(before.SQLType)
	mapped, err := typedef.ReownDomainDatum(null, after.SQLType, nil, imports, nil)
	if err != nil || mapped.Type() != after.SQLType || !mapped.IsNull() || null.Type() != before.SQLType {
		t.Fatal("support NULL destination generation", mapped, err)
	}
	sourceArray, ok := sourceView.LookupArray(before.SQLType)
	if !ok {
		t.Fatal("source companion missing")
	}
	targetArray, ok := view.LookupArray(after.SQLType)
	if !ok {
		t.Fatal("destination companion missing")
	}
	for _, text := range []string{"{}", "{NULL}", "[0:1]={NULL,NULL}"} {
		value, err := sourceArray.Input(t.Context(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		mapped, err := typedef.ReownDomainDatum(value, targetArray, nil, imports, nil)
		if err != nil || mapped.Type() != targetArray || value.Type() != sourceArray {
			t.Fatal("support array destination generation", mapped, err)
		}
		if got, err := targetArray.Output(t.Context(), mapped); err != nil || got != text {
			t.Fatal("destination array codec", got, err)
		}
	}
	if err := engine.Reset(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	if literal, err := before.SQLType.SQLLiteral(t.Context(), null); err != nil || literal != "NULL" {
		t.Fatal("retained source NULL descriptor", literal, err)
	}
	if target.Catalog.State.Get(namespace.Address()) != namespace {
		t.Fatal("import replaced live destination TEMP authority")
	}
}
