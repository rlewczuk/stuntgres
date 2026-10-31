package core_test

import (
	"context"
	"reflect"
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
)

func TestConstraintRenameAuthorization(t *testing.T) {
	for _, declaration := range []string{"a INT CONSTRAINT old NOT NULL", "a INT CONSTRAINT old CHECK(a>0)", "a INT CONSTRAINT old UNIQUE", "a INT CONSTRAINT old PRIMARY KEY", "a INT,CONSTRAINT old EXCLUDE(a WITH =)"} {
		t.Run(declaration, func(t *testing.T) {
			e, admin, owner, ns, acl := renameAuthorityFixture(t)
			enginetest.SessionSQL(t, owner, "DROP TABLE protected.t")
			enginetest.SessionSQL(t, owner, "CREATE TABLE protected.t("+declaration+")")
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
			tx := enginetest.TransactionBegin(t, admin)
			enginetest.SessionSQL(t, admin, "LOCK TABLE protected.t IN ACCESS SHARE MODE")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := enginetest.LockAsync(e, owner, ctx, "ALTER TABLE protected.t RENAME CONSTRAINT old TO new", false)
			enginetest.LockPending(t, e, 1)
			enginetest.TransactionEnd(t, admin, tx, false)
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
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT old NOT NULL)")
	ref := roleTableRef(t, e, "t")
	before := e.Catalog.State.MustRelation(ref.Address)
	definition := before.Constraints.NotNull[0]
	storage := e.Storage.Relations[ref.Address]
	enginetest.SessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	after := e.Catalog.State.MustRelation(ref.Address)
	renamed := after.Constraints.NotNull[0]
	renamed.Name = definition.Name
	if after == before || before.Constraints.NotNull[0].Name != "old" || !reflect.DeepEqual(renamed, definition) || !reflect.DeepEqual(after.Columns, before.Columns) || &after.Attributes[0] != &before.Attributes[0] || e.Storage.Relations[ref.Address] != storage {
		t.Fatal("NOT NULL rename changed stable attribute/enforcement/storage")
	}
}

func TestConstraintRenamePrivateRoots(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(k INT CONSTRAINT old UNIQUE)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1)")
	ref := roleTableRef(t, e, "t")
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "ALTER TABLE t ALTER k TYPE BIGINT")
	earlier := s.Transactions.Catalog
	parent := earlier.Descriptors[ref.Address].(*catalog.Relation)
	idx := parent.Constraints.Unique[0].Index
	root := index.JournalIndexesOrZero(earlier).UniqueRoots[idx]
	storage := earlier.Storage[ref.Address].Replacement
	if root == nil || storage == nil {
		t.Fatal("TYPE did not establish genuine private roots")
	}
	enginetest.SessionSQL(t, s, "SAVEPOINT saved")
	enginetest.SessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT old TO new")
	renamed := s.Transactions.Catalog
	if renamed == earlier || index.JournalIndexesOrZero(renamed).UniqueRoots[idx] != root || renamed.Storage[ref.Address].Replacement != storage || earlier.Descriptors[idx].Name() != "old" {
		t.Fatal("rename copied/mutated prior private roots")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO saved")
	if s.Transactions.Catalog != earlier {
		t.Fatal("private root savepoint pointer lost")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}

// Ordinary table owner in a separately owned namespace: USAGE and CREATE are
// independent host grants, so CREATE can be revoked without hiding the source.
func renameAuthorityFixture(t *testing.T) (*core.Engine, *session.Session, *session.Session, ids.ObjectRef, authorization.ACLValue) {
	t.Helper()
	e := engine.New()
	admin, owner := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, admin, "CREATE USER rename_transition_owner")
	enginetest.SessionSQL(t, admin, "CREATE SCHEMA protected")
	actor, _ := e.Roles().Lookup("rename_transition_owner")
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "protected"})
	ns := e.Catalog.Domain.Ref(view.Epoch(), address)
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: view.MustNamespace(address).Owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}
	if err := enginetest.SetObjectACL(e, context.Background(), ns, acl); err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, owner, "SET SESSION AUTHORIZATION rename_transition_owner")
	enginetest.SessionSQL(t, owner, "CREATE TABLE protected.t(a INT CONSTRAINT nn NOT NULL,b TEXT)")
	enginetest.SessionSQL(t, owner, "INSERT INTO protected.t VALUES(7,'keep')")
	return e, admin, owner, ns, acl
}
