package tables_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func TestConcurrentIndexU5AbsentRootPrivateTypeAndOwnPrefix(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(1)")
	concurrentUnitError(t, s, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t((n+1))", "23505")
	concurrentUnitIndex(t, e, "ui", false, false)
	enginetest.SessionSQL(t, s, "DELETE FROM t")
	enginetest.SessionSQL(t, s, "BEGIN ISOLATION LEVEL READ COMMITTED")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(2)")
	enginetest.SessionSQL(t, s, "ALTER TABLE t ALTER n TYPE BIGINT")
	// Public committed observation cannot see private reconstruction; same-TX
	// producers must nevertheless use the real rebuilt private tuple baseline.
	concurrentUnitIndex(t, e, "ui", false, false)
	enginetest.SessionSQL(t, s, "SAVEPOINT typed")
	concurrentUnitError(t, s, "INSERT INTO t VALUES(2)", "23505")
	enginetest.SessionSQL(t, s, "ROLLBACK TO typed")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(3)")
	enginetest.SessionSQL(t, s, "COMMIT")
	index, root := concurrentUnitIndex(t, e, "ui", true, true)
	if !reflect.DeepEqual(enginetest.BtreeKeys(t, root), []string{"3", "4"}) {
		t.Fatal("sanctioned reconstruction lost pre-TYPE accepted prefix", enginetest.BtreeKeys(t, root))
	}
	concurrentUnitRootMatchesStorage(t, e, index)
	concurrentUnitError(t, s, "INSERT INTO t VALUES(2)", "23505")
	enginetest.LockEmpty(t, e)
}

func TestConcurrentIndexU5TypeChecksWholePrivatePrefixAtExecute(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(1)")
	concurrentUnitError(t, s, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", "23505")
	enginetest.SessionSQL(t, s, "DELETE FROM t")
	enginetest.SessionSQL(t, s, "BEGIN ISOLATION LEVEL READ COMMITTED")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(2),(2)")
	enginetest.SessionSQL(t, s, "SAVEPOINT before_type")
	err := concurrentUnitError(t, s, "ALTER TABLE t ALTER n TYPE BIGINT", "23505")
	enginetest.Diagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui"`, Detail: "Key (n)=(2) is duplicated.", Schema: "public", Table: "t", Constraint: "ui"})
	concurrentUnitIndex(t, e, "ui", false, false)
	enginetest.SessionSQL(t, s, "ROLLBACK TO before_type")
	// Failed TYPE did not retroactively turn the original admission into UNIQUE.
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(2)")
	enginetest.SessionSQL(t, s, "ROLLBACK")
	concurrentUnitIndex(t, e, "ui", false, false)
	enginetest.LockEmpty(t, e)
}

func TestConcurrentIndexU5BuildRepairAndTypeDisclosure(t *testing.T) {
	for _, kind := range []string{"simple", "parenthesized", "collated", "computed"} {
		computed := kind == "computed"
		for _, access := range []string{"hidden", "key_columns", "table"} {
			t.Run(kind+"/"+access, func(t *testing.T) {
				e := engine.New()
				admin, adminDone := enginetest.RoleOperation(t, e, "postgres")
				defer adminDone()
				enginetest.RoleExecute(t, e, admin, "CREATE ROLE index_owner LOGIN")
				owner, ownerDone := enginetest.RoleOperation(t, e, "index_owner")
				defer ownerDone()
				enginetest.WithPublicSchemaUnitOwner(t, e, "index_owner", func() {
					enginetest.RoleExecute(t, e, owner, "CREATE TABLE t(v TEXT,n INT,payload TEXT)")
				})
				seed := "INSERT INTO t VALUES('A',1,'protected'),('a',1,'different')"
				if kind == "collated" {
					seed = "INSERT INTO t VALUES('A',1,'protected'),('A',1,'different')"
				}
				enginetest.RoleExecute(t, e, owner, seed)
				ref := enginetest.RoleTableRef(t, e, "t")
				actor, ok := e.Roles().Lookup("index_owner")
				if !ok || actor.Superuser {
					t.Fatal("nonsuperuser owner fixture", actor, ok)
				}
				grant := func(attribute int32, privileges authorization.Privileges) {
					target := ref
					target.Address.SubID = attribute
					acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: actor.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: privileges}}}
					if err := enginetest.SetObjectACL(e, context.Background(), target, acl); err != nil {
						t.Fatal(err)
					}
				}
				// REINDEX separately requires MAINTAIN; this fixture revokes
				// SELECT, not the authority needed to reach duplicate checking.
				grant(0, authorization.InsertPrivilege|authorization.MaintainPrivilege)
				if access == "table" {
					grant(0, authorization.InsertPrivilege|authorization.MaintainPrivilege|authorization.SelectPrivilege)
				}
				if access == "key_columns" {
					grant(1, authorization.SelectPrivilege)
					grant(2, authorization.SelectPrivilege)
				}
				definition := "n,n"
				detail := "Key (n, n)=(1, 1) is duplicated."
				typeDetail := "Key (n, n)=(0, 0) is duplicated."
				writeDetail := "Key (n, n)=(1, 1) already exists."
				if kind == "parenthesized" {
					definition = "(n),(n)"
				}
				if kind == "collated" {
					definition = `(v) COLLATE "C" text_ops,n`
					detail = "Key (v, n)=(A, 1) is duplicated."
					typeDetail = "Key (v, n)=(A, 0) is duplicated."
					writeDetail = "Key (v, n)=(A, 1) already exists."
				}
				if computed {
					definition = `lower(v) COLLATE "C" text_ops,(n+1)`
					detail = "Key (lower(v), (n + 1))=(a, 2) is duplicated."
					typeDetail = "Key (lower(v), (n + 1))=(a, 1) is duplicated."
					writeDetail = "Key (lower(v), (n + 1))=(a, 2) already exists."
				}
				if access == "hidden" || (computed && access != "table") {
					detail, typeDetail, writeDetail = "Duplicate keys exist.", "Duplicate keys exist.", ""
				}
				var err error
				enginetest.WithPublicSchemaUnitOwner(t, e, "index_owner", func() {
					_, err = e.Execute(owner, enginetest.Statement(t, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t("+definition+") INCLUDE(payload)"), 0, nil)
				})
				enginetest.Diagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui"`, Detail: detail, Schema: "public", Table: "t", Constraint: "ui"})
				initial, _ := concurrentUnitIndex(t, e, "ui", false, false)
				_, err = e.Execute(owner, enginetest.Statement(t, "REINDEX INDEX ui"), 0, nil)
				enginetest.Diagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui"`, Detail: detail, Schema: "public", Table: "t", Constraint: "ui"})
				before := e.CatalogRead().State()
				_, err = e.Execute(owner, enginetest.Statement(t, "ALTER TABLE t ALTER n TYPE BIGINT USING 0"), 0, nil)
				enginetest.Diagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui"`, Detail: typeDetail, Schema: "public", Table: "t", Constraint: "ui"})
				unchanged, _ := concurrentUnitIndex(t, e, "ui", false, false)
				if e.CatalogRead().State() != before || unchanged != initial {
					t.Fatal("failed standalone TYPE changed committed authority")
				}
				enginetest.RoleExecute(t, e, admin, "DELETE FROM t")
				enginetest.RoleExecute(t, e, admin, "INSERT INTO t VALUES('A',1,'protected')")
				enginetest.RoleExecute(t, e, owner, "REINDEX INDEX ui")
				healthy, _ := concurrentUnitIndex(t, e, "ui", true, true)
				concurrentUnitRootMatchesStorage(t, e, healthy)
				write := "INSERT INTO t VALUES('a',1,'different')"
				if kind == "collated" {
					write = "INSERT INTO t VALUES('A',1,'different')"
				}
				_, err = e.Execute(owner, enginetest.Statement(t, write), 0, nil)
				enginetest.Diagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "ui"`, Detail: writeDetail, Schema: "public", Table: "t", Constraint: "ui"})
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func concurrentUnitError(t *testing.T, s *session.Session, query, code string) error {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	result, err := core.EngineOf(s).Execute(op, enginetest.Statement(t, query), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code {
		t.Fatalf("%s: %v; want %s", query, err, code)
	}
	return err
}

func concurrentUnitIndex(t *testing.T, e *core.Engine, name string, ready, valid bool) (*catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	namespace := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name})
	index, ok := e.Catalog.State.Relation(address)
	if !ok || index.SQLIndex == nil || index.SQLIndex.Ready != ready || index.SQLIndex.Valid != valid || !index.SQLIndex.Live {
		t.Fatalf("index %s has unexpected phase: %#v", name, index)
	}
	return index, e.Storage.IndexRoots[address]
}

func concurrentUnitRootMatchesStorage(t *testing.T, e *core.Engine, idx *catalog.Relation) {
	t.Helper()
	e.Mu.Lock()
	parent := e.Catalog.State.MustRelation(idx.SQLIndex.Parent)
	snapshot := *e.Storage.Relations[parent.Address()]
	root := e.Storage.IndexRoots[idx.Address()]
	e.Mu.Unlock()
	expected, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, idx.SQLIndex, &snapshot, nil)
	if err != nil || root == nil || expected == nil {
		t.Fatalf("could not compare installed logical root with live row-version build: %v", err)
	}
	if !reflect.DeepEqual(root, expected) {
		t.Fatalf("installed logical root differs from actual live row-version build: %v", err)
	}
}

// Reduced storage fixtures still need real family members and genuine callable
// declarations. Share an immutable ordinary engine bootstrap, never a provider
// fallback or a test callback.
func operatorFamilyTestRead() catalog.Read { return operatorFamilyUnitSeed() }

var operatorFamilyUnitSeed = sync.OnceValue(func() catalog.Read { return engine.New().CatalogRead() })
