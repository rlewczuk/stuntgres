package sqltype

import "testing"

func TestBuiltinCompositeCompletion(t *testing.T) {
	types := make(map[ID]*Descriptor)
	for _, d := range Builtins() { types[d.ID()] = d }
	row, array := types[12144], types[12143]
	if row.Concrete() || array.Concrete() || array.Element() != row { t.Fatal("fixture must start as a lookup-only builtin pair") }
	names := [...]string{"name", "ident", "type", "level", "path", "total_bytes", "total_nblocks", "free_bytes", "free_chunks", "used_bytes"}
	ids := [...]ID{25, 25, 25, 23, 1007, 20, 20, 20, 20, 20}
	members := make([]CompositeMember, len(names))
	for i, name := range names { members[i] = CompositeMember{Attribute: int32(i+1), Name: name, Usage: types[ids[i]].Usage()} }
	completed, companion, err := CompleteBuiltinComposite(row, array, members)
	if err != nil { t.Fatal(err) }
	if completed == row || companion == array || !completed.Concrete() || !completed.IsComposite() || !completed.IsBuiltin() || completed.IsUserDefined() || completed.ID() != row.ID() || completed.Name() != row.Name() || completed.SQLName() != row.SQLName() || completed.DatabaseName() != row.DatabaseName() || completed.NamespaceName() != row.NamespaceName() || completed.AssociatedArray() != array.ID() {
		t.Fatal("builtin row provenance lost", completed)
	}
	if !companion.Concrete() || !companion.IsBuiltin() || companion.Element() != completed || companion.ID() != array.ID() || companion.DatabaseName() != array.DatabaseName() || companion.NamespaceName() != array.NamespaceName() {
		t.Fatal("builtin companion provenance/link lost", companion)
	}
	for i, member := range members { if completed.CompositeMember(i) != member { t.Fatal("member declaration changed", i) } }
	members[0].Name = "changed input"
	if completed.CompositeMember(0).Name != "name" || row.Concrete() || row.IsComposite() || array.Concrete() || array.Element() != row {
		t.Fatal("completion mutated source descriptors or borrowed member slice")
	}
	foreign := make(map[ID]*Descriptor)
	for _, d := range Builtins() { foreign[d.ID()] = d }
	for _, pair := range [][2]*Descriptor{{nil, array}, {row, nil}, {types[25], types[1009]}, {LookupOnly(row.ID(), row.Name(), array.ID()), array}, {row, foreign[12143]}, {row, types[12356]}} {
		if a, b, err := CompleteBuiltinComposite(pair[0], pair[1], members); err == nil || a != nil || b != nil { t.Fatal("invalid/noncanonical builtin pair accepted", pair, a, b, err) }
	}
	members[0].Name = "name"
	members[3].Usage = foreign[23].Usage()
	if a, b, err := CompleteBuiltinComposite(row, array, members); err == nil || a != nil || b != nil { t.Fatal("mixed member generations accepted", a, b, err) }
	// Completing an engine's copies must not promote the process builtin template.
	for _, d := range Builtins() { if (d.ID() == row.ID() || d.ID() == array.ID()) && d.Concrete() { t.Fatal("process builtin template mutated", d) } }
}
