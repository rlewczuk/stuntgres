package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func TestIsnManifestRoles(t *testing.T) {
	e, ext, _ := isnUnitInstalled(t)
	before := e.CatalogRead()
	for _, slots := range [][2]int{{0,3},{1,4},{2,5},{1,2},{4,5},{0,6},{3,6},{1,7},{2,8},{7,8},{0,9},{3,9},{6,9},{1,10},{7,10},{2,11},{8,11},{10,11},{0,12},{9,12},{13,14},{0,15},{3,15},{6,15},{9,15},{12,15},{1,16},{4,16},{7,16},{10,16},{13,16},{2,17},{5,17},{8,17},{11,17},{14,17},{16,17}} {
		candidate := catalog.CloneState(before.State())
		changed := *ext; changed.Members = append([]ids.ObjectAddress(nil), ext.Members...)
		changed.Members[slots[0]], changed.Members[slots[1]] = changed.Members[slots[1]], changed.Members[slots[0]]
		candidate.SetDescriptor(changed.Address(), &changed)
		if err := extension.StockIsn.ValidateExtension(e.catalog.Read(candidate), &changed); err == nil { t.Fatal("swapped manifest roles accepted", slots) }
	}
	for slot := range len(ext.Members) {
		for _, missing := range []bool{false, true} {
			candidate := catalog.CloneState(before.State())
			changed := *ext
			changed.Members = append([]ids.ObjectAddress(nil), ext.Members...)
			if missing {
				changed.Members = append(changed.Members[:slot], changed.Members[slot+1:]...)
			} else {
				changed.Members[slot] = changed.Members[(slot+1)%len(changed.Members)]
			}
			candidate.SetDescriptor(changed.Address(), &changed)
			if err := extension.StockIsn.ValidateExtension(e.catalog.Read(candidate), &changed); err == nil {
				t.Fatal("missing or duplicate manifest role accepted", slot, missing)
			}
		}
	}
	for _, slot := range []int{0,3,6,9,12,15} {
		base := before.MustType(ext.Members[slot])
		other := before.MustType(ext.Members[(slot+3)%18])
		input := "9780123456780!"; if slot == 0 { input = "1234567890120!" }
		if slot == 9 || slot == 12 { input = "M123456780!" }
		if slot == 15 { input = "9771234567898!" }
		value, err := base.SQLType.Input(context.Background(), input, -1); if err != nil { t.Fatal(err) }
		for _, source := range []sqltype.Value{value, sqltype.Null(base.SQLType)} {
			if _, err := typedef.ReownDomainDatum(source, other.SQLType, nil, map[*sqltype.Descriptor]*sqltype.Descriptor{base.SQLType: other.SQLType}, nil); err == nil { t.Fatal("source authorization crossed ISN family") }
		}
		candidate := catalog.CloneState(before.State())
		array := *candidate.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
		array.SQLType = sqltype.LookupArray(array.SQLType.ID(), array.Name(), other.SQLType)
		candidate.SetDescriptor(array.Address(), &array)
		if err := extension.StockIsn.ValidateExtension(e.catalog.Read(candidate), ext); err == nil { t.Fatal("cross-family companion accepted") }
		for _, offset := range []int{1,2} {
			codec := before.MustProcedure(ext.Members[slot+offset])
			source := [...]string{"", "ean13_in", "ean13_out"}[offset]
			if slot == 3 { source = [...]string{"", "isbn_in", "isn_out"}[offset] }
			if slot == 6 { source = [...]string{"", "isbn_in", "ean13_out"}[offset] }
			if slot == 9 { source = [...]string{"", "ismn_in", "isn_out"}[offset] }
			if slot == 12 { source = [...]string{"", "ismn_in", "ean13_out"}[offset] }
			if slot == 15 { source = [...]string{"", "issn_in", "isn_out"}[offset] }
			if codec.Display.Source != source { t.Fatal("wrong immutable codec source", slot, offset, codec.Display.Source) }
			candidate := catalog.CloneState(before.State())
			proc := *candidate.MustProcedure(ext.Members[slot+offset])
			proc.Display.Source = "foreign_symbol"
			candidate.SetDescriptor(proc.Address(), &proc)
			if err := extension.StockIsn.ValidateExtension(e.catalog.Read(candidate), ext); err == nil { t.Fatal("forged codec declaration accepted") }
		}
	}
	if e.CatalogRead().State() != before.State() { t.Fatal("rejected role mutations changed catalog") }
}
