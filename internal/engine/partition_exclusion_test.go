package engine

import (
	"context"
	"errors"
	"slices"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Private descriptors exercise coverage and cooked remapping without a second
// public SQL implementation or a synthetic hierarchy provider.
func TestPartitionExclusionCoverage(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	d := *f.parent.SQLIndex
	d.unique = false
	c := exclusionConstraint{OID: 30000, Name: f.parent.name, Index: f.parent.address, IsLocal: true}
	d.constraint = exclusionBacking(&c)
	d.keys = slices.Clone(d.keys)
	d.exclusion = make([]sqltype.ExclusionOperator, len(d.keys))
	for i, key := range d.keys {
		usage, err := indexKeyUsage(f.root, key)
		if err != nil {
			t.Fatal(err)
		}
		class, ok := sqltype.DefaultIndexClass(usage.Type)
		if !ok {
			t.Fatal("missing class")
		}
		d.keys[i].class = class
		d.exclusion[i], err = sqltype.BindExclusionOperator(usage, class, "=")
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := partitionUniqueCoverage(f.root, &d); err != nil {
		t.Fatal(err)
	}
	mapped, err := mapPartitionIndex(context.Background(), f.view, f.root, f.leaf, &d)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapped.exclusion) != len(d.exclusion) || &mapped.exclusion[0] == &d.exclusion[0] {
		t.Fatal("child must own rebound operators")
	}
	for i, key := range mapped.keys {
		usage, err := indexKeyUsage(f.leaf, key)
		if err != nil || !mapped.exclusion[i].MatchesUsage(usage) {
			t.Fatal("child usage", err)
		}
	}
	missing := d
	missing.keys = missing.keys[1:]
	missing.exclusion = missing.exclusion[1:]
	missing.include = []int32{10}
	var diagnostic *sqlstate.Error
	if err := partitionUniqueCoverage(f.root, &missing); !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Detail != `EXCLUDE constraint on table "unit_root" lacks column "a" which is part of the partition key.` {
		t.Fatal("INCLUDE cannot cover exclusion key", err)
	}
	// A genuinely independent leaf declaration is not widened to ancestor scope.
	if err := partitionUniqueCoverage(f.leaf, &missing); err != nil {
		t.Fatal("local scope", err)
	}
	if len(d.keys) != len(f.parent.SQLIndex.keys) || len(d.exclusion) != len(mapped.exclusion) {
		t.Fatal("source definition changed")
	}
}
