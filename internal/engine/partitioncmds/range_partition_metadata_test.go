package partitioncmds_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/sqltype"
)

func rangePartitionRelation(t *testing.T, e *engine.Engine, name string) *catalog.Relation {
	t.Helper()
	descriptor, ok := e.CatalogRead().Descriptor(enginetest.RoleTableRef(t, e, name).Address)
	relation, okRelation := descriptor.(*catalog.Relation)
	if !ok || !okRelation {
		t.Fatalf("missing relation %q: %#v", name, descriptor)
	}
	return relation
}

func rangePartitionIntegerTuple(t *testing.T, keys []catalog.PartitionKeyEntry, integers ...int64) []catalog.PartitionDatum {
	t.Helper()
	if len(keys) != len(integers) {
		t.Fatal("test tuple arity")
	}
	result := make([]catalog.PartitionDatum, len(keys))
	for i, n := range integers {
		value, err := sqltype.Integer(keys[i].Usage.Type, n)
		if err != nil {
			t.Fatal(err)
		}
		result[i] = catalog.PartitionDatum{Value: value}
	}
	return result
}

func TestRangePartitionTypedTupleAndLiveDefault(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE TABLE root(a INT,b INT) PARTITION BY RANGE(a,b)",
		"CREATE TABLE bounded PARTITION OF root FOR VALUES FROM(1,2) TO(3,4)",
		"CREATE TABLE rest PARTITION OF root DEFAULT",
	} {
		enginetest.RoleExecute(t, e, op, query)
	}
	root := rangePartitionRelation(t, e, "root")
	bounded := rangePartitionRelation(t, e, "bounded")
	rest := rangePartitionRelation(t, e, "rest")
	keys := root.Partition.Keys
	for _, test := range []struct {
		left, right      []int64
		order, component int
	}{
		{[]int64{1, 2}, []int64{1, 999}, -1, 1},
		{[]int64{1, 999}, []int64{2, -999}, -1, 0},
		// Equal finite tuples retain the last component for empty-range diagnostics.
		{[]int64{3, 4}, []int64{3, 4}, 0, 1},
		{[]int64{4, -999}, []int64{3, 999}, 1, 0},
	} {
		order, component, err := partition.CompareTuple(context.Background(), keys, rangePartitionIntegerTuple(t, keys, test.left...), rangePartitionIntegerTuple(t, keys, test.right...), sqltype.InputEnvironment{})
		if err != nil || order != test.order || component != test.component {
			t.Fatal(test, order, component, err)
		}
	}
	for _, test := range []struct {
		values   []int64
		explicit bool
	}{
		{[]int64{1, 1}, false}, {[]int64{1, 2}, true}, {[]int64{1, 999}, true},
		{[]int64{2, -999}, true}, {[]int64{3, 3}, true}, {[]int64{3, 4}, false},
	} {
		tuple := rangePartitionIntegerTuple(t, keys, test.values...)
		inside, err := partition.RangeContains(context.Background(), keys, bounded.Membership, tuple, false, sqltype.InputEnvironment{})
		if err != nil || inside != test.explicit {
			t.Fatal(test, inside, err)
		}
		row := []sqltype.Value{tuple[0].Value, tuple[1].Value}
		accepted, err := partition.Accepts(e.CatalogRead(), root, rest, row, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		if err != nil || accepted == test.explicit {
			t.Fatal(test, accepted, err)
		}
	}
	nullRow := []sqltype.Value{sqltype.Null(keys[0].Usage.Type), rangePartitionIntegerTuple(t, keys, 1, 1)[1].Value}
	accepted, err := partition.Accepts(e.CatalogRead(), root, rest, nullRow, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || !accepted {
		t.Fatal("NULL must reach DEFAULT", accepted, err)
	}
	sentinels := []catalog.PartitionDatum{{Kind: -1}, {Kind: -1}}
	finite := rangePartitionIntegerTuple(t, keys, -999, -999)
	order, component, err := partition.CompareTuple(context.Background(), keys, sentinels, finite, sqltype.InputEnvironment{})
	if err != nil || order != -1 || component != 0 {
		t.Fatal(order, component, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := partition.CompareTuple(canceled, keys, finite, finite, sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	retained := e.CatalogRead()
	enginetest.RoleExecute(t, e, op, "DROP TABLE bounded")
	newRoot := rangePartitionRelation(t, e, "root")
	newRest := rangePartitionRelation(t, e, "rest")
	row := []sqltype.Value{finite[0].Value, finite[1].Value}
	for _, catalog := range []catalog.Read{retained, e.CatalogRead()} {
		parent, child := root, rest
		if catalog.State() == e.CatalogRead().State() {
			parent, child = newRoot, newRest
		}
		accepted, err := partition.Accepts(catalog, parent, child, row, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		if err != nil || !accepted {
			t.Fatal(accepted, err)
		}
	}
	formerlyExplicit := rangePartitionIntegerTuple(t, keys, 1, 2)
	accepted, err = partition.Accepts(e.CatalogRead(), newRoot, newRest, []sqltype.Value{formerlyExplicit[0].Value, formerlyExplicit[1].Value}, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || !accepted {
		t.Fatal("DEFAULT did not track removal", accepted, err)
	}
	oldAccepted, err := partition.Accepts(retained, root, rest, []sqltype.Value{formerlyExplicit[0].Value, formerlyExplicit[1].Value}, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || oldAccepted {
		t.Fatal("retained topology changed", oldAccepted, err)
	}
}

func TestRangePartitionDescriptorAdmissionAndNoVirtualHeap(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE TABLE root(a INT,b INT) PARTITION BY RANGE(a,b)",
		"CREATE TABLE middle PARTITION OF root FOR VALUES FROM(1,MINVALUE) TO(3,MAXVALUE) PARTITION BY RANGE(b)",
		"CREATE TABLE leaf PARTITION OF middle DEFAULT",
		"CREATE TABLE rest PARTITION OF root DEFAULT",
	} {
		enginetest.RoleExecute(t, e, op, query)
	}
	root := rangePartitionRelation(t, e, "root")
	middle := rangePartitionRelation(t, e, "middle")
	leaf := rangePartitionRelation(t, e, "leaf")
	rest := rangePartitionRelation(t, e, "rest")
	e.Mu.Lock()
	for _, virtual := range []*catalog.Relation{root, middle} {
		if e.Storage.Relations[virtual.Address()] != nil {
			t.Errorf("virtual relation owns storage: %v", virtual.Address())
		}
	}
	if e.Storage.Relations[leaf.Address()] == nil || e.Storage.Relations[rest.Address()] == nil {
		t.Error("physical leaf lacks storage")
	}
	e.Mu.Unlock()
	for _, relation := range []*catalog.Relation{root, middle, leaf, rest} {
		if err := partition.ValidateDescriptor(e.CatalogRead(), relation); err != nil {
			t.Fatal(relation.Name(), err)
		}
	}
	t.Run("catalog-backed key collation", func(t *testing.T) {
		enginetest.RoleExecute(t, e, op, `CREATE COLLATION rp_admission_coll FROM pg_catalog."C"`)
		enginetest.RoleExecute(t, e, op, `CREATE COLLATION rp_admission_other FROM pg_catalog.pg_unicode_fast`)
		enginetest.RoleExecute(t, e, op, `CREATE TABLE rp_admission_root(k TEXT) PARTITION BY RANGE(k COLLATE rp_admission_coll)`)
		relation := rangePartitionRelation(t, e, "rp_admission_root")
		view := e.CatalogRead()
		key := relation.Partition.Keys[0]
		address := ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Usage.Collation.ID)}
		provider := view.MustCollation(address)
		var other *catalog.Collation
		for _, descriptor := range view.Descriptors() {
			if d, ok := descriptor.(*catalog.Collation); ok && d.Name() == "rp_admission_other" {
				other = d
			}
		}
		if other == nil || other.Semantics == provider.Semantics {
			t.Fatal("distinct owned collation capabilities required")
		}
		if err := partition.ValidateDescriptor(view, relation); err != nil {
			t.Fatal("valid owned key authority", err)
		}
		missing := catalog.CloneState(view.State())
		missing.RemoveDescriptor(address)
		if err := partition.ValidateDescriptor(e.Catalog.Read(missing), relation); err == nil {
			t.Fatal("admitted key whose immutable comparator outlived its provider")
		}
		mismatch := catalog.CloneState(view.State())
		changed := *provider
		changed.Semantics = other.Semantics
		mismatch.SetDescriptor(address, &changed)
		if err := partition.ValidateDescriptor(e.Catalog.Read(mismatch), relation); err == nil {
			t.Fatal("admitted key with a different live provider capability")
		}
		candidate := *relation
		definition := *relation.Partition
		definition.Keys = slices.Clone(definition.Keys)
		definition.Keys[0].Collation = uint32(other.OID)
		candidate.Partition = &definition
		if err := partition.ValidateDescriptor(view, &candidate); err == nil {
			t.Fatal("admitted raw explicit identity inconsistent with effective authority")
		}
		definition.Keys[0].Collation = 0
		if err := partition.ValidateDescriptor(view, &candidate); err != nil {
			t.Fatal("effective authority without explicit clause", err)
		}
	})
	var physical []ids.ObjectAddress
	if err := partition.PhysicalDescendants(e.CatalogRead(), root, func(d *catalog.Relation) error { physical = append(physical, d.Address()); return nil }); err != nil || !reflect.DeepEqual(physical, []ids.ObjectAddress{leaf.Address(), rest.Address()}) {
		t.Fatal(physical, err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*catalog.Relation)
	}{
		{"wrong kind", func(d *catalog.Relation) { d.RelationKind = catalog.TableRelation }},
		{"duplicate child", func(d *catalog.Relation) {
			p := *d.Partition
			p.Children = append(slices.Clone(p.Children), p.Children[0])
			d.Partition = &p
		}},
		{"foreign default", func(d *catalog.Relation) { p := *d.Partition; p.DefaultChild = leaf.Address(); d.Partition = &p }},
		{"no keys", func(d *catalog.Relation) { p := *d.Partition; p.Keys = nil; d.Partition = &p }},
		{"missing comparison", func(d *catalog.Relation) {
			p := *d.Partition
			p.Keys = slices.Clone(p.Keys)
			p.Keys[0].Comparison = sqltype.IndexComparison{}
			d.Partition = &p
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := *root
			test.mutate(&candidate)
			if err := partition.ValidateDescriptor(e.CatalogRead(), &candidate); err == nil {
				t.Fatal("admitted malformed root")
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*catalog.PartitionMembership)
	}{
		{"wrong arity", func(m *catalog.PartitionMembership) { m.Lower = m.Lower[:1] }},
		{"mixed sentinel suffix", func(m *catalog.PartitionMembership) { m.Lower = []catalog.PartitionDatum{{Kind: -1}, {Kind: 1}} }},
		{"empty range", func(m *catalog.PartitionMembership) { m.Lower = slices.Clone(m.Upper) }},
		{"reversed range", func(m *catalog.PartitionMembership) { m.Lower, m.Upper = m.Upper, m.Lower }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := *middle
			membership := *middle.Membership
			candidate.Membership = &membership
			test.mutate(&membership)
			if err := partition.ValidateDescriptor(e.CatalogRead(), &candidate); err == nil {
				t.Fatal("admitted malformed bounds")
			}
		})
	}
	badDefault := *rest
	membership := *rest.Membership
	membership.Lower = rangePartitionIntegerTuple(t, root.Partition.Keys, 0, 0)
	badDefault.Membership = &membership
	if err := partition.ValidateDescriptor(e.CatalogRead(), &badDefault); err == nil {
		t.Fatal("DEFAULT retained explicit tuple")
	}
	cyclic := catalog.CloneState(e.CatalogRead().State())
	cyclicRoot, cyclicMiddle := *root, *middle
	cyclicRoot.Membership = &catalog.PartitionMembership{Parent: middle.Address(), DefaultBound: true}
	middleDefinition := *middle.Partition
	middleDefinition.Children = []ids.ObjectAddress{root.Address()}
	middleDefinition.DefaultChild = root.Address()
	cyclicMiddle.Partition = &middleDefinition
	cyclic.SetDescriptor(root.Address(), &cyclicRoot)
	cyclic.SetDescriptor(middle.Address(), &cyclicMiddle)
	if err := partition.ValidateDescriptor(e.Catalog.Read(cyclic), &cyclicRoot); err == nil {
		t.Fatal("reciprocal hierarchy cycle admitted")
	}
	overlapping := catalog.CloneState(e.CatalogRead().State())
	overlappingRoot, overlappingRest := *root, *rest
	rootDefinition := *root.Partition
	rootDefinition.DefaultChild = ids.ObjectAddress{}
	overlappingRoot.Partition = &rootDefinition
	overlappingRest.Membership = &catalog.PartitionMembership{Parent: root.Address(), Lower: slices.Clone(middle.Membership.Lower), Upper: slices.Clone(middle.Membership.Upper)}
	overlapping.SetDescriptor(root.Address(), &overlappingRoot)
	overlapping.SetDescriptor(rest.Address(), &overlappingRest)
	if err := partition.ValidateDescriptor(e.Catalog.Read(overlapping), &overlappingRoot); err == nil {
		t.Fatal("overlapping catalog child bounds admitted")
	}
	if len(root.Partition.Children) != 2 || root.Partition.DefaultChild != rest.Address() || middle.Membership.Lower[1].Kind != -1 {
		t.Fatal("validation mutated source metadata")
	}
}
