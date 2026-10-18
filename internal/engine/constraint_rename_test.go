package engine

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"sync"
	"testing"
	"time"
)

func TestConstraintRenameImmutableGroup(t *testing.T) {
	for _, declaration := range []string{"k INT CONSTRAINT old UNIQUE", "k INT CONSTRAINT old PRIMARY KEY", "k INT,CONSTRAINT old EXCLUDE(k WITH =) DEFERRABLE"} {
		t.Run(declaration, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t("+declaration+")")
			sessionSQL(t, s, "INSERT INTO t VALUES(1)")
			ref := roleTableRef(t, e, "t")
			parent := e.catalog.state.MustRelation(ref.Address)
			var indexAddress ids.ObjectAddress
			if len(parent.uniqueConstraints) > 0 {
				indexAddress = parent.uniqueConstraints[0].Index
			} else {
				indexAddress = parent.exclusionConstraints[0].Index
			}
			index := e.catalog.state.MustRelation(indexAddress)
			store, root := e.storage[ref.Address], e.indexRoots[indexAddress]
			// Populate representable descriptor comment through the existing stage provider;
			// public COMMENT ON INDEX is intentionally absent.
			copied := *index
			copied.comment = "backing comment"
			stage := e.BeginCatalogStage(e.CatalogRead())
			if err := stage.Replace(&copied); err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: e.catalog.state.epoch}); err != nil {
				t.Fatal(err)
			}
			index = e.catalog.state.MustRelation(indexAddress)
			beforeUnique := slices.Clone(parent.uniqueConstraints)
			beforeExclusion := slices.Clone(parent.exclusionConstraints)
			sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
			after := e.catalog.state.MustRelation(ref.Address)
			backing := e.catalog.state.MustRelation(indexAddress)
			if after == parent || backing == index || after.address != parent.address || backing.address != index.address || e.storage[ref.Address] != store || e.indexRoots[indexAddress] != root {
				t.Fatal("rename copied storage/root or changed group identity")
			}
			if backing.name != "new" || backing.comment != "backing comment" || backing.sqlIndex.constraint.Name != "new" || backing.sqlIndex.constraint.OID != index.sqlIndex.constraint.OID || !backingConstraintPresent(after, backing) {
				t.Fatal("nonreciprocal renamed group")
			}
			if !reflect.DeepEqual(parent.uniqueConstraints, beforeUnique) || !reflect.DeepEqual(parent.exclusionConstraints, beforeExclusion) || index.name != "old" || index.sqlIndex.constraint.Name != "old" {
				t.Fatal("mutated old descriptors")
			}
			if len(after.attributes) > 0 && &after.attributes[0] != &parent.attributes[0] {
				t.Fatal("copied attribute vector")
			}
			if &backing.sqlIndex.keys[0] != &index.sqlIndex.keys[0] {
				t.Fatal("copied immutable keys")
			}
			candidate := *after
			candidate.revision = ids.DefinitionRevision(^uint64(0))
			exhaustedBase := cloneCatalog(e.catalog.state)
			exhaustedBase.setDescriptor(ref.Address, &candidate)
			failed := e.BeginCatalogStage(e.catalog.read(exhaustedBase))
			attempt := *backing
			attempt.name = "again"
			if err := stageBackingIndexRename(failed, &attempt); err == nil || len(failed.descriptors) != 0 || backing.name != "new" {
				t.Fatal("parent revision exhaustion changed group")
			}
			e.DiscardCatalogStage(failed)
			// The local helper checks the same revision exhaustion before slicing.
			candidate.notNullConstraints = []notNullConstraint{{Name: "nn"}}
			if _, err := renameLocalConstraint(&candidate, "nn", "renamed_nn", 'n'); err == nil {
				t.Fatal("revision exhaustion admitted")
			}
			// Ordinary backing-index rename still uses the same group helper.
			sessionSQL(t, s, "ALTER INDEX new RENAME TO again")
			if e.indexRoots[indexAddress] != root || !backingConstraintPresent(e.catalog.state.MustRelation(ref.Address), e.catalog.state.MustRelation(indexAddress)) {
				t.Fatal("ordinary helper caller changed root/group")
			}
		})
	}
}

func TestConstraintRenameCheckOrder(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k INT,CONSTRAINT a CHECK(k>0) NO INHERIT,CONSTRAINT b CHECK(k>1))")
	ref := roleTableRef(t, e, "t")
	old := e.catalog.state.MustRelation(ref.Address)
	a, b := old.checkConstraints[0], old.checkConstraints[1]
	next, err := renameLocalConstraint(old, "a", "z", 'c')
	if err != nil {
		t.Fatal(err)
	}
	if old.checkConstraints[0].Name != "a" || next.checkConstraints[0].Name != "b" || next.checkConstraints[1].Name != "z" {
		t.Fatal("lexical order or immutable vector")
	}
	renamed := next.checkConstraints[1]
	renamed.Name = a.Name
	if !reflect.DeepEqual(renamed, a) || !reflect.DeepEqual(next.checkConstraints[0], b) {
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
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Index(dump.String(), `CONSTRAINT "z"`) > strings.Index(dump.String(), `CONSTRAINT "b"`) {
		t.Fatal("Dump replaced declaration ordinal with enforcement order", dump.String())
	}
}

func TestConstraintRenamePrivateOverlay(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT old UNIQUE)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1)")
	ref := roleTableRef(t, e, "t")
	old := e.catalog.state.MustRelation(ref.Address)
	index := old.uniqueConstraints[0].Index
	store, root := e.storage[ref.Address], e.indexRoots[index]
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	tx := s.transactions
	first := tx.catalog
	if first == nil || first.originals[ref.Address] != old || e.catalog.state.Get(ref.Address) != old {
		t.Fatal("private overlay/original authority absent")
	}
	sessionSQL(t, s, "SAVEPOINT saved")
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT new TO later")
	second := tx.catalog
	if second == first || first.descriptors[index].Name() != "new" || second.descriptors[index].Name() != "later" || second.originals[ref.Address] != old {
		t.Fatal("repeated rename mutated saved overlay")
	}
	sessionSQL(t, s, "ROLLBACK TO saved")
	if tx.catalog != first {
		t.Fatal("savepoint did not restore exact overlay")
	}
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT new TO old")
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	if tx.catalog.originals[ref.Address] != old || e.storage[ref.Address] != store || e.indexRoots[index] != root {
		t.Fatal("name reuse lost originals/storage/root")
	}
	sessionSQL(t, s, "COMMIT")
	if e.storage[ref.Address] != store || e.indexRoots[index] != root {
		t.Fatal("metadata commit copied roots")
	}
}

func TestConstraintRenamePublicationFences(t *testing.T) {
	for _, phase := range []string{"validation", "finish"} {
		for _, change := range []string{"cancel", "reset", "restore", "collision", "descriptor", "authorization"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				e := New()
				s := sessionStart(t, e, nil)
				sessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT old UNIQUE)")
				ref := roleTableRef(t, e, "t")
				old := e.catalog.state.MustRelation(ref.Address)
				index := old.uniqueConstraints[0].Index
				backing := e.catalog.state.Get(index)
				store, root := e.storage[ref.Address], e.indexRoots[index]
				var dump bytes.Buffer
				if err := e.Dump(context.Background(), &dump); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				mutate := func() {
					switch change {
					case "cancel":
						cancel()
					case "reset":
						if err := e.Reset(context.Background()); err != nil {
							t.Fatal(err)
						}
						run(t, e, "CREATE TABLE t(k INT CONSTRAINT old UNIQUE)")
					case "restore":
						if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
					case "collision":
						run(t, e, "CREATE TABLE new(k INT)")
					case "descriptor":
						e.mu.Lock()
						replacement := *old
						replacement.revision++
						replacement.comment = "concurrent"
						state := cloneCatalog(e.catalog.state)
						state.setDescriptor(ref.Address, &replacement)
						e.catalog.state = state
						e.mu.Unlock()
					case "authorization":
						run(t, e, "CREATE ROLE changed_authority")
					}
				}
				provider := e.catalog.providers[ids.RelationClassID]
				if phase == "validation" {
					var once sync.Once
					e.catalog.providers[ids.RelationClassID] = alterDropProvider{CatalogProvider: provider, callback: func() { once.Do(func() { e.catalog.providers[ids.RelationClassID] = provider; mutate() }) }}
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
				_, err = scope.Execute(statement(t, "ALTER TABLE t RENAME CONSTRAINT old TO new"), 0, nil)
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
					if !errors.Is(err, ErrCatalogReplaced) {
						t.Fatal(err)
					}
					return
				}
				if e.catalog.state.Get(index) != backing || e.storage[ref.Address] != store || e.indexRoots[index] != root {
					t.Fatal("failed rename partially published backing/storage")
				}
				if change != "descriptor" && e.catalog.state.Get(ref.Address) != old {
					t.Fatal("failed rename published parent")
				}
			})
		}
	}
}

func TestConstraintRenameDeferredIdentity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k INT,CONSTRAINT old EXCLUDE(k WITH =) DEFERRABLE)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1)")
	parent := e.catalog.state.MustRelation(roleTableRef(t, e, "t").Address)
	oid := parent.exclusionConstraints[0].OID
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "SET CONSTRAINTS old DEFERRED")
	sessionSQL(t, s, "INSERT INTO t VALUES(1)")
	sessionSQL(t, s, "SAVEPOINT saved")
	modes := s.transactions.exclusionModes
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	if !s.transactions.exclusionModes.byConstraint[oid] || len(s.transactions.exclusionModes.events) != 1 || s.transactions.exclusionModes.events[0].constraint != oid {
		t.Fatal("rename reset selected mode/event")
	}
	sessionSQL(t, s, "ROLLBACK TO saved")
	if !reflect.DeepEqual(s.transactions.exclusionModes, modes) {
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
			if err := e.SetObjectACL(context.Background(), ns, acl); err != nil {
				t.Fatal(err)
			}
			op, err := owner.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw := statement(t, "ALTER TABLE protected.t RENAME CONSTRAINT old TO new")
			if _, err := e.Describe(op, raw); err != nil {
				t.Fatal("syntax-only Prepare", err)
			}
			op.Done()
			tx := transactionBegin(t, admin)
			sessionSQL(t, admin, "LOCK TABLE protected.t IN ACCESS SHARE MODE")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := lockAsync(e, owner, ctx, "ALTER TABLE protected.t RENAME CONSTRAINT old TO new", false)
			lockPending(t, e, 1)
			transactionEnd(t, admin, tx, false)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal("owner USAGE/no CREATE rename", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			lockEmpty(t, e)
		})
	}
}

func TestConstraintRenameNotNullIdentity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT old NOT NULL)")
	ref := roleTableRef(t, e, "t")
	before := e.catalog.state.MustRelation(ref.Address)
	definition := before.notNullConstraints[0]
	storage := e.storage[ref.Address]
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	after := e.catalog.state.MustRelation(ref.Address)
	renamed := after.notNullConstraints[0]
	renamed.Name = definition.Name
	if after == before || before.notNullConstraints[0].Name != "old" || !reflect.DeepEqual(renamed, definition) || !reflect.DeepEqual(after.columns, before.columns) || &after.attributes[0] != &before.attributes[0] || e.storage[ref.Address] != storage {
		t.Fatal("NOT NULL rename changed stable attribute/enforcement/storage")
	}
}

func TestConstraintRenamePrivateRoots(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT old UNIQUE)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1)")
	ref := roleTableRef(t, e, "t")
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "ALTER TABLE t ALTER k TYPE BIGINT")
	earlier := s.transactions.catalog
	parent := earlier.descriptors[ref.Address].(*relationDescriptor)
	index := parent.uniqueConstraints[0].Index
	root := earlier.uniqueRoots[index]
	storage := earlier.storage[ref.Address].replacement
	if root == nil || storage == nil {
		t.Fatal("TYPE did not establish genuine private roots")
	}
	sessionSQL(t, s, "SAVEPOINT saved")
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	renamed := s.transactions.catalog
	if renamed == earlier || renamed.uniqueRoots[index] != root || renamed.storage[ref.Address].replacement != storage || earlier.descriptors[index].Name() != "old" {
		t.Fatal("rename copied/mutated prior private roots")
	}
	sessionSQL(t, s, "ROLLBACK TO saved")
	if s.transactions.catalog != earlier {
		t.Fatal("private root savepoint pointer lost")
	}
	sessionSQL(t, s, "ROLLBACK")
}
