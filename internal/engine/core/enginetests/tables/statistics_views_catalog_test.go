package tables_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestStatisticsViewsCatalogAdmission(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	for _, contract := range []struct {
		oid        ids.OID
		row, array sqltype.ID
		width      int
		slots      map[int]string
	}{
		{pgcatalog.StatsOID, 12055, 12054, 17, map[int]string{7: "most_common_vals", 9: "histogram_bounds", 11: "most_common_elems", 14: "range_length_histogram", 16: "range_bounds_histogram"}},
		{pgcatalog.StatsExtOID, 12060, 12059, 15, nil},
		{pgcatalog.StatsExtExprsOID, 12065, 12064, 17, map[int]string{10: "most_common_vals", 12: "histogram_bounds", 14: "most_common_elems"}},
	} {
		source := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: contract.oid})
		if err := core.RelationProvider.Validate(read, source); err != nil {
			t.Fatal("released descriptor rejected", source.Name(), err)
		}
		row, _ := read.LookupType(contract.row)
		array, _ := read.LookupType(contract.array)
		rowObject := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(contract.row)})
		arrayObject := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(contract.array)})
		if source.RowType != contract.row || array.Element() != row || rowObject.OwningRelation != source.Address() || arrayObject.OwningRelation != source.Address() || rowObject.Array != contract.array || len(source.Columns) != contract.width || !source.VirtualView || !source.CatalogView {
			t.Fatal("nominal source ownership", source.Name())
		}
		for i, column := range source.Columns {
			if !column.Nullable || column.DeclaredDimensions != 0 || column.Usage.Typmod != -1 {
				t.Fatal("derived metadata changed", source.Name(), i, column)
			}
			name, pseudo := contract.slots[i]
			if pseudo && (column.Name != name || column.Usage.Type.ID() != 2277 || !column.Usage.CatalogNullUsage()) {
				t.Fatal("released ANYARRAY identity", i, column)
			}
			if len(contract.slots) == 0 {
				continue
			}
			bad := *source
			bad.Columns = slices.Clone(source.Columns)
			if pseudo {
				bad.Columns[i].Name = "lookalike"
				bad.Index = make(map[string]int, len(source.Index))
				for key, slot := range source.Index {
					if slot != i {
						bad.Index[key] = slot
					}
				}
				bad.Index["lookalike"] = i
			} else {
				anyarray, _ := read.LookupType(2277)
				bad.Columns[i].Usage = anyarray.Usage()
			}
			if err := core.RelationProvider.Validate(read, &bad); err == nil {
				t.Fatal("ANYARRAY escaped its exact released attribute", source.Name(), i)
			}
		}
		if len(contract.slots) == 0 {
			continue
		}
		for _, mutate := range []func(*catalog.Relation){
			func(r *catalog.Relation) { r.ObjectAddress.ObjectID = 49123 },
			func(r *catalog.Relation) { r.ObjectNamespace.ObjectID = ids.PublicNamespaceOID },
			func(r *catalog.Relation) { r.ObjectName = "lookalike" },
			func(r *catalog.Relation) { r.VirtualView = false },
			func(r *catalog.Relation) {
				r.RelationKind = catalog.TableRelation
				r.VirtualView = false
				r.CatalogView = false
			},
		} {
			bad := *source
			mutate(&bad)
			state := catalog.CloneState(read.State())
			state.UnbindName(catalog.DescriptorNameKey(source))
			state.SetDescriptor(bad.Address(), &bad)
			state.BindName(catalog.DescriptorNameKey(&bad), bad.Address())
			if err := core.RelationProvider.Validate(read.WithState(state), &bad); err == nil {
				t.Fatal("lookalike/stored ANYARRAY admitted", source.Name(), bad.Address(), bad.Namespace(), bad.Name())
			}
		}
		foreign := engine.New().CatalogRead()
		anyarray, _ := foreign.LookupType(2277)
		bad := *source
		bad.Columns = slices.Clone(source.Columns)
		for slot := range contract.slots {
			bad.Columns[slot].Usage = anyarray.Usage()
			break
		}
		if err := core.RelationProvider.Validate(read, &bad); err == nil {
			t.Fatal("same-OID foreign descriptor generation admitted")
		}
	}
}
