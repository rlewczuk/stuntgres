package indexes_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
)

func TestConstraintRenameImmutableGroup(t *testing.T) {
	for _, declaration := range []string{"k INT CONSTRAINT old UNIQUE", "k INT CONSTRAINT old PRIMARY KEY", "k INT,CONSTRAINT old EXCLUDE(k WITH =) DEFERRABLE"} {
		t.Run(declaration, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t("+declaration+")")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1)")
			ref := enginetest.RoleTableRef(t, e, "t")
			parent := e.Catalog.State.MustRelation(ref.Address)
			var indexAddress ids.ObjectAddress
			if len(parent.Constraints.Unique) > 0 {
				indexAddress = parent.Constraints.Unique[0].Index
			} else {
				indexAddress = parent.Constraints.Exclusion[0].Index
			}
			idx := e.Catalog.State.MustRelation(indexAddress)
			store, root := e.Storage.Relations[ref.Address], e.Storage.IndexRoots[indexAddress]
			// Populate representable descriptor comment through the existing stage provider;
			// public COMMENT ON INDEX is intentionally absent.
			copied := *idx
			copied.Comment = "backing comment"
			stage := e.BeginCatalogStage(e.CatalogRead())
			if err := stage.Replace(&copied); err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: e.Catalog.State.Epoch()}); err != nil {
				t.Fatal(err)
			}
			idx = e.Catalog.State.MustRelation(indexAddress)
			beforeUnique := slices.Clone(parent.Constraints.Unique)
			beforeExclusion := slices.Clone(parent.Constraints.Exclusion)
			enginetest.SessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
			after := e.Catalog.State.MustRelation(ref.Address)
			backing := e.Catalog.State.MustRelation(indexAddress)
			if after == parent || backing == idx || after.Address() != parent.Address() || backing.Address() != idx.Address() || e.Storage.Relations[ref.Address] != store || e.Storage.IndexRoots[indexAddress] != root {
				t.Fatal("rename copied storage/root or changed group identity")
			}
			if backing.Name() != "new" || backing.Comment != "backing comment" || backing.SQLIndex.Constraint.Name != "new" || backing.SQLIndex.Constraint.OID != idx.SQLIndex.Constraint.OID || !index.BackingConstraintPresent(after, backing) {
				t.Fatal("nonreciprocal renamed group")
			}
			if !reflect.DeepEqual(parent.Constraints.Unique, beforeUnique) || !reflect.DeepEqual(parent.Constraints.Exclusion, beforeExclusion) || idx.Name() != "old" || idx.SQLIndex.Constraint.Name != "old" {
				t.Fatal("mutated old descriptors")
			}
			if len(after.Attributes) > 0 && &after.Attributes[0] != &parent.Attributes[0] {
				t.Fatal("copied attribute vector")
			}
			if &backing.SQLIndex.Keys[0] != &idx.SQLIndex.Keys[0] {
				t.Fatal("copied immutable keys")
			}
			candidate := *after
			candidate.ObjectRevision = ids.DefinitionRevision(^uint64(0))
			exhaustedBase := catalog.CloneState(e.Catalog.State)
			exhaustedBase.SetDescriptor(ref.Address, &candidate)
			failed := e.BeginCatalogStage(e.Catalog.Read(exhaustedBase))
			attempt := *backing
			attempt.ObjectName = "again"
			if err := index.StageBackingIndexRename(failed, &attempt); err == nil || failed.DescriptorCount() != 0 || backing.Name() != "new" {
				t.Fatal("parent revision exhaustion changed group")
			}
			e.DiscardCatalogStage(failed)
			// The local helper checks the same revision exhaustion before slicing.
			candidate.Constraints.NotNull = []catalog.NotNullConstraint{{Name: "nn"}}
			if _, err := table.RenameLocalConstraint(&candidate, "nn", "renamed_nn", 'n'); err == nil {
				t.Fatal("revision exhaustion admitted")
			}
			// Ordinary backing-index rename still uses the same group helper.
			enginetest.SessionSQL(t, s, "ALTER INDEX new RENAME TO again")
			if e.Storage.IndexRoots[indexAddress] != root || !index.BackingConstraintPresent(e.Catalog.State.MustRelation(ref.Address), e.Catalog.State.MustRelation(indexAddress)) {
				t.Fatal("ordinary helper caller changed root/group")
			}
		})
	}
}

func TestConstraintRenamePrivateOverlay(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT old UNIQUE)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1)")
	ref := enginetest.RoleTableRef(t, e, "t")
	old := e.Catalog.State.MustRelation(ref.Address)
	index := old.Constraints.Unique[0].Index
	store, root := e.Storage.Relations[ref.Address], e.Storage.IndexRoots[index]
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	tx := s.Transactions
	first := tx.Catalog
	if first == nil || first.Originals[ref.Address] != old || e.Catalog.State.Get(ref.Address) != old {
		t.Fatal("private overlay/original authority absent")
	}
	enginetest.SessionSQL(t, s, "SAVEPOINT saved")
	enginetest.SessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT new TO later")
	second := tx.Catalog
	if second == first || first.Descriptors[index].Name() != "new" || second.Descriptors[index].Name() != "later" || second.Originals[ref.Address] != old {
		t.Fatal("repeated rename mutated saved overlay")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO saved")
	if tx.Catalog != first {
		t.Fatal("savepoint did not restore exact overlay")
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT new TO old")
	enginetest.SessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	if tx.Catalog.Originals[ref.Address] != old || e.Storage.Relations[ref.Address] != store || e.Storage.IndexRoots[index] != root {
		t.Fatal("name reuse lost originals/storage/root")
	}
	enginetest.SessionSQL(t, s, "COMMIT")
	if e.Storage.Relations[ref.Address] != store || e.Storage.IndexRoots[index] != root {
		t.Fatal("metadata commit copied roots")
	}
}
