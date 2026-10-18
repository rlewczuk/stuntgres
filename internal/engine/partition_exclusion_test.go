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
	d.Unique = false
	c := exclusionConstraint{OID: 30000, Name: f.parent.name, Index: f.parent.address, IsLocal: true}
	d.Constraint = exclusionBacking(&c)
	d.Keys = slices.Clone(d.Keys)
	d.Exclusion = make([]sqltype.ExclusionOperator, len(d.Keys))
	for i, key := range d.Keys {
		usage, err := indexKeyUsage(f.root, key)
		if err != nil {
			t.Fatal(err)
		}
		class, ok := sqltype.DefaultIndexClass(usage.Type)
		if !ok {
			t.Fatal("missing class")
		}
		d.Keys[i].Class = class
		d.Exclusion[i], err = sqltype.BindExclusionOperator(usage, class, "=")
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
	if len(mapped.Exclusion) != len(d.Exclusion) || &mapped.Exclusion[0] == &d.Exclusion[0] {
		t.Fatal("child must own rebound operators")
	}
	for i, key := range mapped.Keys {
		usage, err := indexKeyUsage(f.leaf, key)
		if err != nil || !mapped.Exclusion[i].MatchesUsage(usage) {
			t.Fatal("child usage", err)
		}
	}
	missing := d
	missing.Keys = missing.Keys[1:]
	missing.Exclusion = missing.Exclusion[1:]
	missing.Include = []int32{10}
	var diagnostic *sqlstate.Error
	if err := partitionUniqueCoverage(f.root, &missing); !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Detail != `EXCLUDE constraint on table "unit_root" lacks column "a" which is part of the partition key.` {
		t.Fatal("INCLUDE cannot cover exclusion key", err)
	}
	// A genuinely independent leaf declaration is not widened to ancestor scope.
	if err := partitionUniqueCoverage(f.leaf, &missing); err != nil {
		t.Fatal("local scope", err)
	}
	if len(d.Keys) != len(f.parent.SQLIndex.Keys) || len(d.Exclusion) != len(mapped.Exclusion) {
		t.Fatal("source definition changed")
	}
}
