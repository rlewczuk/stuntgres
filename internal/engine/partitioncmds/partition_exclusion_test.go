package partitioncmds_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Private descriptors exercise coverage and cooked remapping without a second
// public SQL implementation or a synthetic hierarchy provider.
func TestPartitionExclusionCoverage(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	d := *f.parent.SQLIndex
	d.Unique = false
	c := catalog.ExclusionConstraint{OID: 30000, Name: f.parent.Name(), Index: f.parent.Address(), IsLocal: true}
	d.Constraint = index.ExclusionBacking(&c)
	d.Keys = slices.Clone(d.Keys)
	d.Exclusion = make([]sqltype.ExclusionOperator, len(d.Keys))
	for i, key := range d.Keys {
		usage, err := index.KeyUsage(f.root, key)
		if err != nil {
			t.Fatal(err)
		}
		class, ok := sqltype.DefaultIndexClass(usage.Type)
		if !ok {
			t.Fatal("missing class")
		}
		d.Keys[i].Class = class
		d.Exclusion[i], err = operatorFamilyTestExclusion(usage, class, "=")
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := index.PartitionUniqueCoverage(f.view, f.root, &d); err != nil {
		t.Fatal(err)
	}
	mapped, err := index.MapPartitionIndex(context.Background(), f.view, f.root, f.leaf, &d)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapped.Exclusion) != len(d.Exclusion) || &mapped.Exclusion[0] == &d.Exclusion[0] {
		t.Fatal("child must own rebound operators")
	}
	for i, key := range mapped.Keys {
		usage, err := index.KeyUsage(f.leaf, key)
		if err != nil || !mapped.Exclusion[i].MatchesUsage(usage) {
			t.Fatal("child usage", err)
		}
	}
	missing := d
	missing.Keys = missing.Keys[1:]
	missing.Exclusion = missing.Exclusion[1:]
	missing.Include = []int32{10}
	var diagnostic *sqlstate.Error
	if err := index.PartitionUniqueCoverage(f.view, f.root, &missing); !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Detail != `EXCLUDE constraint on table "unit_root" lacks column "a" which is part of the partition key.` {
		t.Fatal("INCLUDE cannot cover exclusion key", err)
	}
	// A genuinely independent leaf declaration is not widened to ancestor scope.
	if err := index.PartitionUniqueCoverage(f.view, f.leaf, &missing); err != nil {
		t.Fatal("local scope", err)
	}
	if len(d.Keys) != len(f.parent.SQLIndex.Keys) || len(d.Exclusion) != len(mapped.Exclusion) {
		t.Fatal("source definition changed")
	}
}

func operatorFamilyTestExclusion(usage sqltype.Usage, class sqltype.IndexClass, name string) (sqltype.ExclusionOperator, error) {
	return routine.BindExclusionOperator(operatorFamilyTestRead(), usage, class, name)
}
