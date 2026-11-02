package control

import (
	"maps"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestReplacementDumpPrioritySimultaneousRemap(t *testing.T) {
	address := rangeIdentityAddress
	a, b, c, d := address(30000), address(30010), address(30020), address(30100)
	for _, tc := range []struct {
		name    string
		mapping map[ids.ObjectAddress]ids.ObjectAddress
		want    []map[ids.ObjectAddress]ids.OID
	}{
		{
			name:    "cycle",
			mapping: map[ids.ObjectAddress]ids.ObjectAddress{a: b, b: c, c: a},
			want: []map[ids.ObjectAddress]ids.OID{
				{a: 30020, b: 11, c: 22, d: 44},
				{a: 22, b: 30020, c: 11, d: 44},
			},
		},
		{
			name:    "occupied_destination_chain",
			mapping: map[ids.ObjectAddress]ids.ObjectAddress{a: b, b: c, c: address(30200)},
			want: []map[ids.ObjectAddress]ids.OID{
				{b: 11, c: 22, address(30200): 30020, d: 44},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, _, _, _ := fdwUnitSource()
			original := catalog.NewState(1)
			for i, a := range []ids.ObjectAddress{a, b, c, d} {
				namespace := rangeIdentityNamespace(a.ObjectID)
				namespace.ObjectName = []string{"a", "b", "c", "d"}[i]
				original.SetDescriptor(a, namespace)
				original.BindName(catalog.DescriptorNameKey(namespace), a)
			}
			original.SetDumpPriority(a, 11)
			original.SetDumpPriority(b, 22)
			original.SetDumpPriority(d, 44)
			next := catalog.CloneState(original)
			for round, want := range tc.want {
				if err := RemapReplacementIdentities(source.CatalogView, next, nil, tc.mapping, nil, nil, nil); err != nil {
					t.Fatal(err)
				}
				if got := maps.Collect(next.DumpPriorities()); !maps.Equal(got, want) {
					t.Fatalf("round %d priorities=%v, want %v", round, got, want)
				}
				for address, priority := range want {
					name := map[ids.OID]string{11: "a", 22: "b", 30020: "c", 44: "d"}[priority]
					if next.MustNamespace(address).Name() != name {
						t.Fatal("priority no longer follows its imported descriptor", address, priority)
					}
				}
			}
			if original.MustNamespace(a).Name() != "a" || original.MustNamespace(b).Name() != "b" || original.MustNamespace(c).Name() != "c" || original.Get(address(30200)) != nil {
				t.Fatal("remap mutated retained source descriptors")
			}
			if got := maps.Collect(original.DumpPriorities()); !maps.Equal(got, map[ids.ObjectAddress]ids.OID{a: 11, b: 22, d: 44}) {
				t.Fatal("remap mutated retained source priorities", got)
			}
		})
	}
}
