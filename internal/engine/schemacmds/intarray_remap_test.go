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

func TestIntarraySetRemap(t *testing.T) {
	source, original, _ := intarrayUnitInstalled(t)
	sv := source.CatalogRead()
	target := engine.New()
	op, done := extensionTemporaryOperation(t, target, "postgres")
	defer done()
	namespace := intarrayUnitTemporaryNamespace(t, target, op)
	extensionUnitPublish(t, target, op, intarrayUnitStage(t, target, op, "unaccent", namespace.Name()))
	if _, err := target.ReplaceCatalog(t.Context(), source, sv); err != nil {
		t.Fatal(err)
	}
	tv := target.CatalogRead()
	destination := tv.MustExtension(tv.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"}))
	if destination.Installation == original.Installation || len(destination.Installation.Members) != 44 || tv.Owner().Domain == sv.Owner().Domain {
		t.Fatal("destination retained source installation")
	}
	displacedProcedure := false
	for slot := 6; slot < 22; slot++ {
		before := sv.MustProcedure(original.Installation.Members[slot])
		after := tv.MustProcedure(destination.Installation.Members[slot])
		if after == before || tv.Get(after.Address()) != after || !after.Origin.Valid(after.Address()) || after.Origin.Installation != destination.Installation || after.Origin.Declaration != slot || after.Namespace() != destination.TargetNamespace || after.Signature != before.Signature || after.Result != before.Result || after.Owner != destination.Owner {
			t.Fatal("procedure destination role", slot)
		}
		// Only collisions move numeric addresses. Every declaration still
		// acquires the destination's descriptor, installation and domain.
		displacedProcedure = displacedProcedure || after.Address() != before.Address()
		if _, err := extension.ProcedureSymbol(tv, after); err != nil {
			t.Fatal(err)
		}
		if _, err := extension.ProcedureSymbol(tv, before); err == nil {
			t.Fatal("source artifact acquired destination authority")
		}
		if _, err := extension.ProcedureSymbol(sv, before); err != nil {
			t.Fatal("source mutated", err)
		}
	}
	if !displacedProcedure {
		t.Fatal("TEMP spacer did not displace any selected procedure")
	}
	for i := range 10 {
		before := sv.MustOperator(original.Installation.Members[22+i])
		after := tv.MustOperator(destination.Installation.Members[22+i])
		if after == before || after.Function != destination.Installation.Members[6+i].ObjectID || after.Left != 1007 || after.Right != before.Right || after.Result != before.Result || after.Origin.Installation != destination.Installation || after.Owner != destination.Owner {
			t.Fatal("operator destination role", i)
		}
		if i < 3 && (after.Restriction != destination.Installation.Members[16+i].ObjectID || after.Join != destination.Installation.Members[19+i].ObjectID) {
			t.Fatal("estimator references retained source identity")
		}
		if before.Commutator != 0 {
			for j := range 10 {
				if before.Commutator == original.Installation.Members[22+j].ObjectID && after.Commutator != destination.Installation.Members[22+j].ObjectID {
					t.Fatal("commutator retained source identity")
				}
			}
		}
		if before.Function != original.Installation.Members[6+i].ObjectID {
			t.Fatal("source graph mutated")
		}
	}
	if err := extension.StockIntarray.ValidateExtension(tv, destination); err != nil {
		t.Fatal(err)
	}
	if target.Catalog.State.Get(namespace.Address()) != namespace {
		t.Fatal("destination TEMP authority displaced")
	}
}
