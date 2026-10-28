package core_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
)

func TestConstraintRenameImmutableGroup(t *testing.T) {
	for _, declaration := range []string{"k INT CONSTRAINT old UNIQUE", "k INT CONSTRAINT old PRIMARY KEY", "k INT,CONSTRAINT old EXCLUDE(k WITH =) DEFERRABLE"} {
		t.Run(declaration, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t("+declaration+")")
			sessionSQL(t, s, "INSERT INTO t VALUES(1)")
			ref := roleTableRef(t, e, "t")
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
			sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
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
			sessionSQL(t, s, "ALTER INDEX new RENAME TO again")
			if e.Storage.IndexRoots[indexAddress] != root || !index.BackingConstraintPresent(e.Catalog.State.MustRelation(ref.Address), e.Catalog.State.MustRelation(indexAddress)) {
				t.Fatal("ordinary helper caller changed root/group")
			}
		})
	}
}

func TestConstraintRenameCheckOrder(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k INT,CONSTRAINT a CHECK(k>0) NO INHERIT,CONSTRAINT b CHECK(k>1))")
	ref := roleTableRef(t, e, "t")
	old := e.Catalog.State.MustRelation(ref.Address)
	a, b := old.Constraints.Check[0], old.Constraints.Check[1]
	next, err := table.RenameLocalConstraint(old, "a", "z", 'c')
	if err != nil {
		t.Fatal(err)
	}
	if old.Constraints.Check[0].Name != "a" || next.Constraints.Check[0].Name != "b" || next.Constraints.Check[1].Name != "z" {
		t.Fatal("lexical order or immutable vector")
	}
	renamed := next.Constraints.Check[1]
	renamed.Name = a.Name
	if !reflect.DeepEqual(renamed, a) || !reflect.DeepEqual(next.Constraints.Check[0], b) {
		t.Fatal("CHECK expression/OID/dependency/ordinal changed")
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(next); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err != nil {
		t.Fatal("real CHECK provider rejected sorted vector", err)
	}
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT a TO z")
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Index(dump.String(), `CONSTRAINT "z"`) > strings.Index(dump.String(), `CONSTRAINT "b"`) {
		t.Fatal("Dump replaced declaration ordinal with enforcement order", dump.String())
	}
}

func TestConstraintRenamePrivateOverlay(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT old UNIQUE)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1)")
	ref := roleTableRef(t, e, "t")
	old := e.Catalog.State.MustRelation(ref.Address)
	index := old.Constraints.Unique[0].Index
	store, root := e.Storage.Relations[ref.Address], e.Storage.IndexRoots[index]
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	tx := s.Transactions
	first := tx.Catalog
	if first == nil || first.Originals[ref.Address] != old || e.Catalog.State.Get(ref.Address) != old {
		t.Fatal("private overlay/original authority absent")
	}
	sessionSQL(t, s, "SAVEPOINT saved")
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT new TO later")
	second := tx.Catalog
	if second == first || first.Descriptors[index].Name() != "new" || second.Descriptors[index].Name() != "later" || second.Originals[ref.Address] != old {
		t.Fatal("repeated rename mutated saved overlay")
	}
	sessionSQL(t, s, "ROLLBACK TO saved")
	if tx.Catalog != first {
		t.Fatal("savepoint did not restore exact overlay")
	}
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT new TO old")
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	if tx.Catalog.Originals[ref.Address] != old || e.Storage.Relations[ref.Address] != store || e.Storage.IndexRoots[index] != root {
		t.Fatal("name reuse lost originals/storage/root")
	}
	sessionSQL(t, s, "COMMIT")
	if e.Storage.Relations[ref.Address] != store || e.Storage.IndexRoots[index] != root {
		t.Fatal("metadata commit copied roots")
	}
}

func TestConstraintRenamePublicationFences(t *testing.T) {
	for _, phase := range []string{"validation", "finish"} {
		for _, change := range []string{"cancel", "reset", "restore", "collision", "descriptor", "authorization"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				e := engine.New()
				s := sessionStart(t, e, nil)
				sessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT old UNIQUE)")
				ref := roleTableRef(t, e, "t")
				old := e.Catalog.State.MustRelation(ref.Address)
				index := old.Constraints.Unique[0].Index
				backing := e.Catalog.State.Get(index)
				store, root := e.Storage.Relations[ref.Address], e.Storage.IndexRoots[index]
				var dump bytes.Buffer
				if err := engine.Dump(context.Background(), e, &dump); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				mutate := func() {
					switch change {
					case "cancel":
						cancel()
					case "reset":
						if err := engine.Reset(context.Background(), e); err != nil {
							t.Fatal(err)
						}
						enginetest.Run(t, e, "CREATE TABLE t(k INT CONSTRAINT old UNIQUE)")
					case "restore":
						if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
					case "collision":
						enginetest.Run(t, e, "CREATE TABLE new(k INT)")
					case "descriptor":
						e.Mu.Lock()
						replacement := *old
						replacement.ObjectRevision++
						replacement.Comment = "concurrent"
						state := catalog.CloneState(e.Catalog.State)
						state.SetDescriptor(ref.Address, &replacement)
						e.Catalog.PublishLocked(state)
						e.Mu.Unlock()
					case "authorization":
						enginetest.Run(t, e, "CREATE ROLE changed_authority")
					}
				}
				provider := e.Catalog.Providers[ids.RelationClassID]
				if phase == "validation" {
					var once sync.Once
					e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{Provider: provider, callback: func() { once.Do(func() { e.Catalog.Providers[ids.RelationClassID] = provider; mutate() }) }}
				}
				op, err := s.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				_, err = scope.Execute(enginetest.Statement(t, "ALTER TABLE t RENAME CONSTRAINT old TO new"), 0, nil)
				if phase == "finish" {
					if err != nil {
						scope.Finish(err)
						t.Fatal(err)
					}
					mutate()
				}
				err = scope.Finish(err)
				if err == nil {
					t.Fatal("publication fence absent")
				}
				if change == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if change == "reset" || change == "restore" {
					if !errors.Is(err, ids.ErrCatalogReplaced) {
						t.Fatal(err)
					}
					return
				}
				if e.Catalog.State.Get(index) != backing || e.Storage.Relations[ref.Address] != store || e.Storage.IndexRoots[index] != root {
					t.Fatal("failed rename partially published backing/storage")
				}
				if change != "descriptor" && e.Catalog.State.Get(ref.Address) != old {
					t.Fatal("failed rename published parent")
				}
			})
		}
	}
}

func TestConstraintRenameDeferredIdentity(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k INT,CONSTRAINT old EXCLUDE(k WITH =) DEFERRABLE)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1)")
	parent := e.Catalog.State.MustRelation(roleTableRef(t, e, "t").Address)
	oid := parent.Constraints.Exclusion[0].OID
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "SET CONSTRAINTS old DEFERRED")
	sessionSQL(t, s, "INSERT INTO t VALUES(1)")
	sessionSQL(t, s, "SAVEPOINT saved")
	modes := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	if !core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)).ByConstraint[oid] || len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)).Events) != 1 || core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)).Events[0].Constraint != oid {
		t.Fatal("rename reset selected mode/event")
	}
	sessionSQL(t, s, "ROLLBACK TO saved")
	if !reflect.DeepEqual(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)), modes) {
		t.Fatal("rollback lost deferred obligation")
	}
	sessionSQL(t, s, "ROLLBACK")
}

func TestConstraintRenameAuthorization(t *testing.T) {
	for _, declaration := range []string{"a INT CONSTRAINT old NOT NULL", "a INT CONSTRAINT old CHECK(a>0)", "a INT CONSTRAINT old UNIQUE", "a INT CONSTRAINT old PRIMARY KEY", "a INT,CONSTRAINT old EXCLUDE(a WITH =)"} {
		t.Run(declaration, func(t *testing.T) {
			e, admin, owner, ns, acl := renameAuthorityFixture(t)
			sessionSQL(t, owner, "DROP TABLE protected.t")
			sessionSQL(t, owner, "CREATE TABLE protected.t("+declaration+")")
			acl.Entries[0].Privileges = authorization.UsagePrivilege
			if err := enginetest.SetObjectACL(e, context.Background(), ns, acl); err != nil {
				t.Fatal(err)
			}
			op, err := owner.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw := enginetest.Statement(t, "ALTER TABLE protected.t RENAME CONSTRAINT old TO new")
			if _, err := e.Describe(op, raw); err != nil {
				t.Fatal("syntax-only Prepare", err)
			}
			op.Done()
			tx := transactionBegin(t, admin)
			sessionSQL(t, admin, "LOCK TABLE protected.t IN ACCESS SHARE MODE")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := enginetest.LockAsync(e, owner, ctx, "ALTER TABLE protected.t RENAME CONSTRAINT old TO new", false)
			enginetest.LockPending(t, e, 1)
			transactionEnd(t, admin, tx, false)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal("owner USAGE/no CREATE rename", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestConstraintRenameNotNullIdentity(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT old NOT NULL)")
	ref := roleTableRef(t, e, "t")
	before := e.Catalog.State.MustRelation(ref.Address)
	definition := before.Constraints.NotNull[0]
	storage := e.Storage.Relations[ref.Address]
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	after := e.Catalog.State.MustRelation(ref.Address)
	renamed := after.Constraints.NotNull[0]
	renamed.Name = definition.Name
	if after == before || before.Constraints.NotNull[0].Name != "old" || !reflect.DeepEqual(renamed, definition) || !reflect.DeepEqual(after.Columns, before.Columns) || &after.Attributes[0] != &before.Attributes[0] || e.Storage.Relations[ref.Address] != storage {
		t.Fatal("NOT NULL rename changed stable attribute/enforcement/storage")
	}
}

func TestConstraintRenamePrivateRoots(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT old UNIQUE)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1)")
	ref := roleTableRef(t, e, "t")
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "ALTER TABLE t ALTER k TYPE BIGINT")
	earlier := s.Transactions.Catalog
	parent := earlier.Descriptors[ref.Address].(*catalog.Relation)
	idx := parent.Constraints.Unique[0].Index
	root := index.JournalIndexesOrZero(earlier).UniqueRoots[idx]
	storage := earlier.Storage[ref.Address].Replacement
	if root == nil || storage == nil {
		t.Fatal("TYPE did not establish genuine private roots")
	}
	sessionSQL(t, s, "SAVEPOINT saved")
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	renamed := s.Transactions.Catalog
	if renamed == earlier || index.JournalIndexesOrZero(renamed).UniqueRoots[idx] != root || renamed.Storage[ref.Address].Replacement != storage || earlier.Descriptors[idx].Name() != "old" {
		t.Fatal("rename copied/mutated prior private roots")
	}
	sessionSQL(t, s, "ROLLBACK TO saved")
	if s.Transactions.Catalog != earlier {
		t.Fatal("private root savepoint pointer lost")
	}
	sessionSQL(t, s, "ROLLBACK")
}

type alterDropProvider struct {
	catalog.Provider
	callback func()
}

func (p alterDropProvider) Validate(v catalog.Read, d catalog.Descriptor) error {
	if err := p.Provider.Validate(v, d); err != nil {
		return err
	}
	p.callback()
	return nil
}

// Ordinary table owner in a separately owned namespace: USAGE and CREATE are
// independent host grants, so CREATE can be revoked without hiding the source.
func renameAuthorityFixture(t *testing.T) (*core.Engine, *session.Session, *session.Session, ids.ObjectRef, authorization.ACLValue) {
	t.Helper()
	e := engine.New()
	admin, owner := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, admin, "CREATE USER rename_transition_owner")
	sessionSQL(t, admin, "CREATE SCHEMA protected")
	actor, _ := e.Roles().Lookup("rename_transition_owner")
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "protected"})
	ns := e.Catalog.Domain.Ref(view.Epoch(), address)
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: view.MustNamespace(address).Owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}
	if err := enginetest.SetObjectACL(e, context.Background(), ns, acl); err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, owner, "SET SESSION AUTHORIZATION rename_transition_owner")
	sessionSQL(t, owner, "CREATE TABLE protected.t(a INT CONSTRAINT nn NOT NULL,b TEXT)")
	sessionSQL(t, owner, "INSERT INTO protected.t VALUES(7,'keep')")
	return e, admin, owner, ns, acl
}
