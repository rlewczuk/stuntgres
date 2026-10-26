package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func TestOperatorFamilyProviderInventory(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	if sqltype.IndexClassMetadataCount() != 145 {
		t.Fatal("incomplete released class inventory", sqltype.IndexClassMetadataCount())
	}
	for i := range sqltype.IndexClassMetadataCount() {
		fact, _ := sqltype.IndexClassMetadataAt(i)
		class, ok := view.IndexClass(ids.OID(fact.OID))
		if !ok {
			t.Fatalf("missing live class %d", fact.OID)
		}
		selected, err := routine.BindClassSupport(view, class)
		if class.IsBrin() && !sqltype.BrinMinmaxIdentity(class) {
			state, ok := err.(interface{ SQLState() string })
			if !ok || state.SQLState() != "0A000" {
				t.Fatal("excluded algorithm acquired a provider", fact, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("class %d: %v", fact.OID, err)
		}
		if fact.Method == 783 || fact.Method == 4000 {
			if selected.Support(1).Routine != 0 {
				t.Fatal("logical method acquired a physical callback", fact)
			}
			continue
		}
		slots := 1
		if fact.Method == 405 {
			slots = 2
		}
		if fact.Method == 3580 {
			slots = 4
		}
		for slot := 1; slot <= slots; slot++ {
			support := selected.Support(slot)
			if support.Family != fact.Family || support.Left != fact.Input || support.Right != fact.Input || support.Member == 0 || support.Revision != 1 {
				t.Fatal("incoherent selected support", fact, support)
			}
			if fact.Method == 3580 {
				proc, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(support.Routine)})
				if !ok || proc.BrinSupport != uint8(slot) || catalog.ValidateBrinSupport(proc) != nil {
					t.Fatal("not genuine BRIN summary support", fact, support, proc)
				}
				signatures := [4]string{"2281", "2281,2281,2281,2281", "2281,2281,2281", "2281,2281,2281"}
				results := [4]ids.OID{2281, 16, 16, 16}
				if support.Routine != uint32(3382+slot) || string(proc.Signature) != signatures[slot-1] || proc.Result != results[slot-1] {
					t.Fatal("released internal summary signature", slot, proc)
				}
				if _, scalar := sqltype.NativeIndexRoutineByOID(support.Routine); scalar {
					t.Fatal("summary operation acquired scalar ABI", support)
				}
				continue
			}
			native, ok := sqltype.NativeIndexRoutineByOID(support.Routine)
			declared := routine.BuiltinRoutine(ids.OID(support.Routine))
			if !ok || declared == nil || native.Signature != string(declared.Identity.Signature) || declared.Identity.Namespace != ids.PGCatalogNamespaceOID || !declared.Strict {
				t.Fatal("not genuinely registered", support, native, declared)
			}
		}
	}
	// All comparison members, including mixed-width RI probes, must have a
	// genuine routine association. Optional physical support is metadata-only.
	for _, descriptor := range view.Descriptors() {
		member, ok := descriptor.(*catalog.OperatorFamilySupport)
		if !ok || member.Number != 1 {
			continue
		}
		family, _ := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: member.Family})
		if family.Method != 403 {
			continue
		}
		native, ok := sqltype.NativeIndexRoutineByOID(uint32(member.Function))
		if !ok || native.Kind != sqltype.IndexCompareRoutine || routine.BuiltinRoutine(member.Function) == nil {
			t.Fatal("missing cross comparator", member)
		}
	}
	for _, oid := range []uint32{3130, 3359, 4128, 5051, 6403} {
		if _, ok := sqltype.NativeIndexRoutineByOID(oid); ok {
			t.Fatal("physical callback advertised as native scalar support", oid)
		}
	}
	// Ordinary non-index scalar binding stays independent of class membership.
	integer, _ := view.LookupType(sqltype.Int4)
	if _, err := sqltype.BindOrdering(integer.Usage()); err != nil {
		t.Fatal(err)
	}
	// Existing enum/domain and collation adaptations still bind through released
	// providers, not by deriving a provider from the concrete user type.
	enum, err := sqltype.Enum(90001, "family_unit_enum", "public.family_unit_enum", []string{"z", "a"})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sqltype.Domain(90002, "family_unit_domain", "public.family_unit_domain", enum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []*sqltype.Descriptor{enum, domain} {
		class, _, defaultErr := view.DefaultIndexClass(403, typ)
		if defaultErr != nil {
			t.Fatal(defaultErr)
		}
		comparison, err := routine.BindIndexOrdering(view, typ.Usage(), class)
		if err != nil || comparison.Support().Routine != 3514 {
			t.Fatal("enum/domain comparator", comparison, err)
		}
		hashClass, _, defaultErr := view.DefaultIndexClass(405, typ)
		if defaultErr != nil {
			t.Fatal(defaultErr)
		}
		if _, err := routine.BindPartitionHash(view, typ.Usage(), hashClass); err != nil {
			t.Fatal("enum/domain extended hash", err)
		}
	}
}

func TestOperatorFamilySupportExecution(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	typ, _ := view.LookupType(sqltype.Int4)
	ctx := context.Background()
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(typ, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90010}, ObjectRevision: 1, RelationKind: catalog.TableRelation, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "n", Usage: typ.Usage()}}, Index: map[string]int{"n": 0}}
	class, _ := sqltype.DefaultIndexClass(typ)
	definition := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: class}}, Ready: true, Valid: true, Live: true}
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90011}, ObjectRevision: 1, SQLIndex: definition}
	ordering, err := index.BindIndexOrdering(view, parent, definition)
	if err != nil {
		t.Fatal(err)
	}
	support := ordering.Comparisons[0].Support()
	if class.OID() != 1978 || support != (sqltype.IndexSupport{Family: 1976, Member: 10056, Routine: 351, Left: 23, Right: 23, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}) {
		t.Fatal("actual class/member/routine", class, support)
	}
	for _, c := range []struct {
		a, b int64
		want int
	}{{1, 2, -1}, {2, 2, 0}, {2, 1, 1}} {
		got, err := ordering.Comparisons[0].Compare(ctx, value(c.a), value(c.b), sqltype.InputEnvironment{})
		if err != nil || got != c.want {
			t.Fatal(c, got, err)
		}
	}
	store := &storage.Relation{Versions: []storage.RowVersion{
		{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value(2)}},
		{Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{value(1)}},
		{Row: 3, Version: 3, Born: 1, Values: []sqltype.Value{sqltype.Null(typ)}},
		{Row: 4, Version: 4, Born: 1, Values: []sqltype.Value{value(2)}},
	}}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	root, err := index.BuildIndexRoot(evaluation, parent, definition, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := func(root *storage.IndexRoot) []ids.RowID {
		var result []ids.RowID
		for _, entry := range root.Entries {
			result = append(result, entry.Row)
			if entry.Version != ids.VersionID(entry.Row) {
				t.Fatal("lost version identity", entry)
			}
		}
		return result
	}
	if !reflect.DeepEqual(rows(root), []ids.RowID{2, 1, 4, 3}) {
		t.Fatal("sorted maintained root", rows(root))
	}
	inserted := storage.RowVersion{Row: 5, Version: 5, Values: []sqltype.Value{value(0)}}
	delta := &index.RowPublication{Store: store, Root: store.Versions, Next: append(append([]storage.RowVersion(nil), store.Versions...), inserted), Writes: []storage.RowVersion{inserted}, SingleSlot: -1, Deletion: &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{1: {Version: 1, Ordinal: 0}}}}
	next, err := index.MergeIndexRoot(evaluation, parent, idx, root.Entries, delta, false, true, false, "", sqltype.OutputEnvironment{}, nil, nil)
	if err != nil || !reflect.DeepEqual(rows(next), []ids.RowID{5, 2, 4, 3}) {
		t.Fatal("insert/remove root", next, err)
	}
	if !reflect.DeepEqual(rows(root), []ids.RowID{2, 1, 4, 3}) {
		t.Fatal("mutated retained root")
	}

	hashClass, _ := sqltype.DefaultHashIndexClass(typ)
	hashDefinition := &catalog.IndexDefinition{Method: catalog.HashMethod, MethodOID: 405, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: hashClass}}, Ready: true, Valid: true, Live: true}
	hashIndex := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90012}, ObjectRevision: 1, SQLIndex: hashDefinition}
	hashRoot, err := index.BuildHashIndexRoot(evaluation, parent, hashDefinition, store, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	selected := hashRoot.Hash.Binding.Class.Support(1)
	if selected != (sqltype.IndexSupport{Family: 1977, Member: 10148, Routine: 450, Left: 23, Right: 23, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}) {
		t.Fatal("actual hash member", selected)
	}
	// Independent pinned18.6 vectors, not expected values computed by native.
	for _, c := range []struct {
		n    int64
		want uint32
	}{{-2147483648, 2770616247}, {-1, 385747274}, {0, 4022255791}, {1, 2389907270}, {2, 1134484726}, {42, 1509752520}, {2147483647, 4198209043}} {
		got, err := hashRoot.Hash.Binding.Provider.Hash(ctx, value(c.n))
		if err != nil || got != uint64(c.want) {
			t.Fatal("oracle hash vector", c, got, err)
		}
	}
	if len(hashRoot.Hash.Buckets) != 2 || len(hashRoot.Hash.Buckets[1134484726]) != 2 || len(hashRoot.Hash.Buckets[2389907270]) != 1 {
		t.Fatal("real buckets/duplicates/NULL omission", hashRoot.Hash.Buckets)
	}
	hashNext, err := index.MergeHashIndexRoot(evaluation, parent, hashIndex, hashRoot, delta, false, true)
	if err != nil {
		t.Fatal(err)
	}
	probe := func(root *storage.HashRoot, n int64) []ids.RowID {
		key := value(n)
		h, err := root.Binding.Provider.Hash(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		var found []ids.RowID
		for _, entry := range root.Buckets[h] {
			equal, err := root.Binding.Provider.Equal(ctx, key, entry.Key)
			if err != nil {
				t.Fatal(err)
			}
			if equal {
				found = append(found, entry.Row)
			}
		}
		return found
	}
	if !reflect.DeepEqual(probe(hashNext.Hash, 2), []ids.RowID{4}) || !reflect.DeepEqual(probe(hashNext.Hash, 0), []ids.RowID{5}) || len(probe(hashNext.Hash, 42)) != 0 {
		t.Fatal("maintained hash probes", hashNext.Hash.Buckets)
	}
	if !reflect.DeepEqual(probe(hashRoot.Hash, 2), []ids.RowID{1, 4}) {
		t.Fatal("mutated retained hash buckets")
	}

	// A narrow private descriptor fixture changes only the real selected member;
	// no class fabrication, public pinned replacement, spelling dispatch or test
	// callback. Existing captures remain immutable.
	member, _ := view.FamilySupport(1976, 23, 23, 1)
	replacement := *member
	replacement.Function = 181
	replacement.ObjectRevision++
	candidate := catalog.NewCandidate(view)
	if err := candidate.Replace(&replacement); err != nil {
		t.Fatal(err)
	}
	selectedView := view.WithState(candidate.State())
	subtraction, err := index.BindIndexOrdering(selectedView, parent, definition)
	if err != nil {
		t.Fatal(err)
	}
	cmp, err := subtraction.Comparisons[0].Compare(ctx, value(1), value(3), sqltype.InputEnvironment{})
	original, oldErr := ordering.Comparisons[0].Compare(ctx, value(1), value(3), sqltype.InputEnvironment{})
	if err != nil || oldErr != nil || cmp != -2 || original != -1 || subtraction.Comparisons[0].Support().Routine != 181 {
		t.Fatal("selected routine identity not executed", cmp, original, err, oldErr)
	}
	evaluation.Catalog = selectedView
	if prepared, err := index.BuildIndexRoot(evaluation, parent, definition, store, nil); err != nil || !reflect.DeepEqual(rows(prepared), []ids.RowID{2, 1, 4, 3}) {
		t.Fatal("selected native root preparation", prepared, err)
	}
}
