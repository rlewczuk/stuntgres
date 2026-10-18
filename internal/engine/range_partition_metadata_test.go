package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func rangePartitionRelation(t *testing.T, e *Engine, name string) *relationDescriptor {
	t.Helper()
	descriptor, ok := e.CatalogRead().Descriptor(roleTableRef(t, e, name).Address)
	relation, okRelation := descriptor.(*relationDescriptor)
	if !ok || !okRelation {
		t.Fatalf("missing relation %q: %#v", name, descriptor)
	}
	return relation
}

func rangePartitionIntegerTuple(t *testing.T, keys []partitionKeyEntry, integers ...int64) []partitionDatum {
	t.Helper()
	if len(keys) != len(integers) {
		t.Fatal("test tuple arity")
	}
	result := make([]partitionDatum, len(keys))
	for i, n := range integers {
		value, err := sqltype.Integer(keys[i].usage.Type, n)
		if err != nil {
			t.Fatal(err)
		}
		result[i] = partitionDatum{value: value}
	}
	return result
}

func TestRangePartitionTypedTupleAndLiveDefault(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE TABLE root(a INT,b INT) PARTITION BY RANGE(a,b)",
		"CREATE TABLE bounded PARTITION OF root FOR VALUES FROM(1,2) TO(3,4)",
		"CREATE TABLE rest PARTITION OF root DEFAULT",
	} {
		roleExecute(t, e, op, query)
	}
	root := rangePartitionRelation(t, e, "root")
	bounded := rangePartitionRelation(t, e, "bounded")
	rest := rangePartitionRelation(t, e, "rest")
	keys := root.partition.keys
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
		order, component, err := comparePartitionTuple(context.Background(), keys, rangePartitionIntegerTuple(t, keys, test.left...), rangePartitionIntegerTuple(t, keys, test.right...), sqltype.InputEnvironment{})
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
		inside, err := partitionRangeContains(context.Background(), keys, bounded.membership, tuple, false, sqltype.InputEnvironment{})
		if err != nil || inside != test.explicit {
			t.Fatal(test, inside, err)
		}
		row := []sqltype.Value{tuple[0].value, tuple[1].value}
		accepted, err := partitionAccepts(e.CatalogRead(), root, rest, row, &EvalContext{Context: context.Background()})
		if err != nil || accepted == test.explicit {
			t.Fatal(test, accepted, err)
		}
	}
	nullRow := []sqltype.Value{sqltype.Null(keys[0].usage.Type), rangePartitionIntegerTuple(t, keys, 1, 1)[1].value}
	accepted, err := partitionAccepts(e.CatalogRead(), root, rest, nullRow, &EvalContext{Context: context.Background()})
	if err != nil || !accepted {
		t.Fatal("NULL must reach DEFAULT", accepted, err)
	}
	sentinels := []partitionDatum{{kind: -1}, {kind: -1}}
	finite := rangePartitionIntegerTuple(t, keys, -999, -999)
	order, component, err := comparePartitionTuple(context.Background(), keys, sentinels, finite, sqltype.InputEnvironment{})
	if err != nil || order != -1 || component != 0 {
		t.Fatal(order, component, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := comparePartitionTuple(canceled, keys, finite, finite, sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	retained := e.CatalogRead()
	roleExecute(t, e, op, "DROP TABLE bounded")
	newRoot := rangePartitionRelation(t, e, "root")
	newRest := rangePartitionRelation(t, e, "rest")
	row := []sqltype.Value{finite[0].value, finite[1].value}
	for _, catalog := range []CatalogRead{retained, e.CatalogRead()} {
		parent, child := root, rest
		if catalog.state == e.CatalogRead().state {
			parent, child = newRoot, newRest
		}
		accepted, err := partitionAccepts(catalog, parent, child, row, &EvalContext{Context: context.Background()})
		if err != nil || !accepted {
			t.Fatal(accepted, err)
		}
	}
	formerlyExplicit := rangePartitionIntegerTuple(t, keys, 1, 2)
	accepted, err = partitionAccepts(e.CatalogRead(), newRoot, newRest, []sqltype.Value{formerlyExplicit[0].value, formerlyExplicit[1].value}, &EvalContext{Context: context.Background()})
	if err != nil || !accepted {
		t.Fatal("DEFAULT did not track removal", accepted, err)
	}
	oldAccepted, err := partitionAccepts(retained, root, rest, []sqltype.Value{formerlyExplicit[0].value, formerlyExplicit[1].value}, &EvalContext{Context: context.Background()})
	if err != nil || oldAccepted {
		t.Fatal("retained topology changed", oldAccepted, err)
	}
}

func TestRangePartitionDescriptorAdmissionAndNoVirtualHeap(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE TABLE root(a INT,b INT) PARTITION BY RANGE(a,b)",
		"CREATE TABLE middle PARTITION OF root FOR VALUES FROM(1,MINVALUE) TO(3,MAXVALUE) PARTITION BY RANGE(b)",
		"CREATE TABLE leaf PARTITION OF middle DEFAULT",
		"CREATE TABLE rest PARTITION OF root DEFAULT",
	} {
		roleExecute(t, e, op, query)
	}
	root := rangePartitionRelation(t, e, "root")
	middle := rangePartitionRelation(t, e, "middle")
	leaf := rangePartitionRelation(t, e, "leaf")
	rest := rangePartitionRelation(t, e, "rest")
	e.mu.Lock()
	for _, virtual := range []*relationDescriptor{root, middle} {
		if e.storage[virtual.address] != nil {
			t.Errorf("virtual relation owns storage: %v", virtual.address)
		}
	}
	if e.storage[leaf.address] == nil || e.storage[rest.address] == nil {
		t.Error("physical leaf lacks storage")
	}
	e.mu.Unlock()
	for _, relation := range []*relationDescriptor{root, middle, leaf, rest} {
		if err := validatePartitionDescriptor(e.CatalogRead(), relation); err != nil {
			t.Fatal(relation.name, err)
		}
	}
	t.Run("catalog-backed key collation", func(t *testing.T) {
		roleExecute(t, e, op, `CREATE COLLATION rp_admission_coll FROM pg_catalog."C"`)
		roleExecute(t, e, op, `CREATE COLLATION rp_admission_other FROM pg_catalog.pg_unicode_fast`)
		roleExecute(t, e, op, `CREATE TABLE rp_admission_root(k TEXT) PARTITION BY RANGE(k COLLATE rp_admission_coll)`)
		relation := rangePartitionRelation(t, e, "rp_admission_root")
		view := e.CatalogRead()
		key := relation.partition.keys[0]
		address := ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.usage.Collation.ID)}
		provider := view.MustCollation(address)
		var other *collationDescriptor
		for _, descriptor := range view.Descriptors() {
			if d, ok := descriptor.(*collationDescriptor); ok && d.name == "rp_admission_other" {
				other = d
			}
		}
		if other == nil || other.semantics == provider.semantics {
			t.Fatal("distinct owned collation capabilities required")
		}
		if err := validatePartitionDescriptor(view, relation); err != nil {
			t.Fatal("valid owned key authority", err)
		}
		missing := cloneCatalog(view.state)
		missing.removeDescriptor(address)
		if err := validatePartitionDescriptor(e.catalog.read(missing), relation); err == nil {
			t.Fatal("admitted key whose immutable comparator outlived its provider")
		}
		mismatch := cloneCatalog(view.state)
		changed := *provider
		changed.semantics = other.semantics
		mismatch.setDescriptor(address, &changed)
		if err := validatePartitionDescriptor(e.catalog.read(mismatch), relation); err == nil {
			t.Fatal("admitted key with a different live provider capability")
		}
		candidate := *relation
		definition := *relation.partition
		definition.keys = slices.Clone(definition.keys)
		definition.keys[0].collation = uint32(other.oid)
		candidate.partition = &definition
		if err := validatePartitionDescriptor(view, &candidate); err == nil {
			t.Fatal("admitted raw explicit identity inconsistent with effective authority")
		}
		definition.keys[0].collation = 0
		if err := validatePartitionDescriptor(view, &candidate); err != nil {
			t.Fatal("effective authority without explicit clause", err)
		}
	})
	var physical []ids.ObjectAddress
	if err := partitionPhysicalDescendants(e.CatalogRead(), root, func(d *relationDescriptor) error { physical = append(physical, d.address); return nil }); err != nil || !reflect.DeepEqual(physical, []ids.ObjectAddress{leaf.address, rest.address}) {
		t.Fatal(physical, err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*relationDescriptor)
	}{
		{"wrong kind", func(d *relationDescriptor) { d.relationKind = tableRelation }},
		{"duplicate child", func(d *relationDescriptor) {
			p := *d.partition
			p.children = append(slices.Clone(p.children), p.children[0])
			d.partition = &p
		}},
		{"foreign default", func(d *relationDescriptor) { p := *d.partition; p.defaultChild = leaf.address; d.partition = &p }},
		{"no keys", func(d *relationDescriptor) { p := *d.partition; p.keys = nil; d.partition = &p }},
		{"missing comparison", func(d *relationDescriptor) {
			p := *d.partition
			p.keys = slices.Clone(p.keys)
			p.keys[0].comparison = sqltype.IndexComparison{}
			d.partition = &p
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := *root
			test.mutate(&candidate)
			if err := validatePartitionDescriptor(e.CatalogRead(), &candidate); err == nil {
				t.Fatal("admitted malformed root")
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*partitionMembership)
	}{
		{"wrong arity", func(m *partitionMembership) { m.lower = m.lower[:1] }},
		{"mixed sentinel suffix", func(m *partitionMembership) { m.lower = []partitionDatum{{kind: -1}, {kind: 1}} }},
		{"empty range", func(m *partitionMembership) { m.lower = slices.Clone(m.upper) }},
		{"reversed range", func(m *partitionMembership) { m.lower, m.upper = m.upper, m.lower }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := *middle
			membership := *middle.membership
			candidate.membership = &membership
			test.mutate(&membership)
			if err := validatePartitionDescriptor(e.CatalogRead(), &candidate); err == nil {
				t.Fatal("admitted malformed bounds")
			}
		})
	}
	badDefault := *rest
	membership := *rest.membership
	membership.lower = rangePartitionIntegerTuple(t, root.partition.keys, 0, 0)
	badDefault.membership = &membership
	if err := validatePartitionDescriptor(e.CatalogRead(), &badDefault); err == nil {
		t.Fatal("DEFAULT retained explicit tuple")
	}
	cyclic := cloneCatalog(e.CatalogRead().state)
	cyclicRoot, cyclicMiddle := *root, *middle
	cyclicRoot.membership = &partitionMembership{parent: middle.address, defaultBound: true}
	middleDefinition := *middle.partition
	middleDefinition.children = []ids.ObjectAddress{root.address}
	middleDefinition.defaultChild = root.address
	cyclicMiddle.partition = &middleDefinition
	cyclic.setDescriptor(root.address, &cyclicRoot)
	cyclic.setDescriptor(middle.address, &cyclicMiddle)
	if err := validatePartitionDescriptor(e.catalog.read(cyclic), &cyclicRoot); err == nil {
		t.Fatal("reciprocal hierarchy cycle admitted")
	}
	overlapping := cloneCatalog(e.CatalogRead().state)
	overlappingRoot, overlappingRest := *root, *rest
	rootDefinition := *root.partition
	rootDefinition.defaultChild = ids.ObjectAddress{}
	overlappingRoot.partition = &rootDefinition
	overlappingRest.membership = &partitionMembership{parent: root.address, lower: slices.Clone(middle.membership.lower), upper: slices.Clone(middle.membership.upper)}
	overlapping.setDescriptor(root.address, &overlappingRoot)
	overlapping.setDescriptor(rest.address, &overlappingRest)
	if err := validatePartitionDescriptor(e.catalog.read(overlapping), &overlappingRoot); err == nil {
		t.Fatal("overlapping catalog child bounds admitted")
	}
	if len(root.partition.children) != 2 || root.partition.defaultChild != rest.address || middle.membership.lower[1].kind != -1 {
		t.Fatal("validation mutated source metadata")
	}
}
