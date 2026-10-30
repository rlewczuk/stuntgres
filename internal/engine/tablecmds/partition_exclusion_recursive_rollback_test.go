package tablecmds_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqlstate"
)

// SQL outcomes live in the shared Atomicity scenario. This witness retains the
// real catalog descriptors and physical owners across failed recursive ADD.
func TestPartitionExclusionRecursiveAddRollback(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE TABLE root(k INT) PARTITION BY RANGE(k)",
		"CREATE TABLE leaf PARTITION OF root FOR VALUES FROM(0) TO(10)",
		"CREATE TABLE bad PARTITION OF root DEFAULT",
		"ALTER TABLE leaf ADD CONSTRAINT child_idx EXCLUDE USING btree(k WITH =)",
		"INSERT INTO root VALUES(1),(10),(10)",
	} {
		enginetest.Run(t, e, q)
	}
	before := e.CatalogRead()
	leaf := before.MustRelation(roleTableRef(t, e, "leaf").Address)
	child := before.MustRelation(roleTableRef(t, e, "child_idx").Address)
	original := leaf.Constraints.Exclusion[0]
	backing := e.Storage.IndexRoots[child.Address()]
	store := e.Storage.Relations[leaf.Address()]
	if backing == nil || len(backing.Entries) != 1 || store == nil {
		t.Fatal("missing genuine populated physical owners")
	}
	entries := slices.Clone(backing.Entries)
	entryBacking := &backing.Entries[0]
	snapshot := indexMaintenanceCapture(e)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	_, err := e.Execute(op, enginetest.Statement(t, "ALTER TABLE root ADD CONSTRAINT parent_idx EXCLUDE USING btree(k WITH =)"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23P01" {
		t.Fatal("recursive candidate did not fail its real sibling build", err)
	}
	indexMaintenanceUnchanged(t, e, snapshot)
	if e.CatalogRead().MustRelation(leaf.Address()) != leaf || e.CatalogRead().MustRelation(child.Address()) != child || !reflect.DeepEqual(backing.Entries, entries) || len(backing.Entries) != 1 || &backing.Entries[0] != entryBacking {
		t.Fatal("failed recursive adoption mutated predecessor descriptors or physical entries")
	}
	if original.Parent != 0 || !original.IsLocal || child.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		t.Fatal("failed recursive adoption changed original ancestry")
	}
	enginetest.Run(t, e, "ALTER TABLE root DETACH PARTITION bad")
	enginetest.Run(t, e, "ALTER TABLE root ADD CONSTRAINT parent_idx EXCLUDE USING btree(k WITH =)")
	view := e.CatalogRead()
	adopted := view.MustRelation(leaf.Address())
	parent := view.MustRelation(roleTableRef(t, e, "parent_idx").Address)
	if len(adopted.Constraints.Exclusion) != 1 {
		t.Fatal("retry duplicated the existing provider")
	}
	constraint := adopted.Constraints.Exclusion[0]
	liveIndex := view.MustRelation(child.Address())
	if constraint.OID != original.OID || constraint.Index != original.Index || constraint.Name != original.Name || constraint.Parent != parent.SQLIndex.Constraint.OID || constraint.IsLocal || !constraint.NoInherit || &constraint.Operators[0] != &original.Operators[0] || liveIndex.SQLIndex.PartitionParent != parent.Address() || liveIndex.SQLIndex.Constraint.OID != original.OID {
		t.Fatal("retry failed to reuse original immutable provider authority")
	}
	if e.Storage.Relations[leaf.Address()] != store || e.Storage.IndexRoots[child.Address()] != backing || !reflect.DeepEqual(backing.Entries, entries) || len(backing.Entries) != 1 || &backing.Entries[0] != entryBacking {
		t.Fatal("successful metadata adoption rebuilt physical owners")
	}
	if leaf.Constraints.Exclusion[0].Parent != 0 || !leaf.Constraints.Exclusion[0].IsLocal || child.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		t.Fatal("retry mutated retained predecessor definitions")
	}
	if err := index.ValidateIndexDefinition(view, liveIndex); err != nil {
		t.Fatal(err)
	}
}
