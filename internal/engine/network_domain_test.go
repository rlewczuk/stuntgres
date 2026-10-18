package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestNetworkDomainCatalogRemap(t *testing.T) {
	for _, tc := range []struct {
		typ, input, output string
		id                 sqltype.ID
	}{
		{"inet", "192.168.0.129/25", "192.168.0.129/25", sqltype.Inet},
		{"cidr", "10", "10.0.0.0/8", sqltype.Cidr},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.network_direct AS "+tc.typ))
			domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.network_chain AS public.network_direct"))
			view := e.CatalogRead()
			direct := domainUnitType(t, view, "network_direct")
			chain := domainUnitType(t, view, "network_chain")
			arrays := make([]*typeDescriptor, 2)
			for i, domain := range []*typeDescriptor{direct, chain} {
				array, ok := view.LookupArray(domain.typ)
				if !ok || array.Concrete() || !array.IsArray() || array.Element() != domain.typ || array.ID() != domain.array {
					t.Fatal("lookup-only reciprocal", domain, array, ok)
				}
				arrays[i] = view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(domain.array)})
				if _, err := sqltype.Array(61000, "_forged", domain.typ); err == nil {
					t.Fatal("recursive network array constructor admitted")
				}
			}
			if chain.typ.DomainBase().Type != direct.typ || chain.typ.BaseType().ID() != tc.id || !direct.typ.Concrete() || !chain.typ.Concrete() {
				t.Fatal("scalar chain identity", direct, chain)
			}
			table := domainUnitTable(t, e, op, "CREATE TABLE public.network_saved(v public.network_chain)")
			value, err := chain.typ.Input(context.Background(), tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			original := &relationStorage{generation: 2, versions: []rowVersion{{row: 1, version: 2, born: 1, values: []sqltype.Value{value}}}}
			storage := map[ids.ObjectAddress]*relationStorage{table.address: original}
			next := cloneCatalog(e.CatalogRead().state)
			physical, _ := New().CatalogRead().LookupType(tc.id)
			builtinAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(tc.id)}
			builtin := *next.MustType(builtinAddress)
			builtin.typ = physical
			next.setDescriptor(builtinAddress, &builtin)
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{
				direct.Address(): {ClassID: ids.TypeClassID, ObjectID: 60001}, chain.Address(): {ClassID: ids.TypeClassID, ObjectID: 60002},
				arrays[0].Address(): {ClassID: ids.TypeClassID, ObjectID: 60003}, arrays[1].Address(): {ClassID: ids.TypeClassID, ObjectID: 60004},
			}
			changed, err := reownDomainTypes(next, mapping, false)
			if err != nil || !changed {
				t.Fatal(changed, err)
			}
			if err := reownCatalogScalarTypes(next, storage, mapping); err != nil {
				t.Fatal(err)
			}
			importedDirect := next.MustType(direct.Address())
			importedChain := next.MustType(chain.Address())
			if importedDirect.typ.ID() != 60001 || importedChain.typ.ID() != 60002 || importedChain.typ.DomainBase().Type != importedDirect.typ || importedChain.typ.BaseType() != physical {
				t.Fatal("remapped chain identity", importedDirect, importedChain)
			}
			for i, domain := range []*typeDescriptor{importedDirect, importedChain} {
				array := next.MustType(arrays[i].Address())
				if array.typ.Concrete() || !array.typ.IsArray() || array.typ.Element() != domain.typ || array.typ.ID() != sqltype.ID(60003+i) || domain.array != array.typ.ID() {
					t.Fatal("remap expanded reciprocal array authority", domain, array)
				}
				if _, err := sqltype.Array(61001, "_forged", domain.typ); err == nil {
					t.Fatal("remapped array constructor admitted")
				}
			}
			restored := storage[table.address]
			if restored == original || restored.versions[0].values[0].Type() != importedChain.typ || next.MustRelation(table.address).Columns[0].Usage.Type != importedChain.typ {
				t.Fatal("scalar storage remap")
			}
			out, err := importedChain.typ.Output(context.Background(), restored.versions[0].values[0])
			if err != nil || out != tc.output {
				t.Fatal(out, err)
			}
			if original.versions[0].values[0].Type() != chain.typ || arrays[0].typ.Element() != direct.typ || arrays[1].typ.Element() != chain.typ || arrays[0].typ.Concrete() || arrays[1].typ.Concrete() {
				t.Fatal("source authority mutated")
			}
		})
	}
}
