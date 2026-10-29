package core_test

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
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
	if !reflect.DeepEqual(btreeKeys(t, root), []string{"3", "4"}) {
		t.Fatal("sanctioned reconstruction lost pre-TYPE accepted prefix", btreeKeys(t, root))
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
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui"`, Detail: "Key (n)=(2) is duplicated.", Schema: "public", Table: "t", Constraint: "ui"})
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
				withPublicSchemaUnitOwner(t, e, "index_owner", func() {
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
				withPublicSchemaUnitOwner(t, e, "index_owner", func() {
					_, err = e.Execute(owner, enginetest.Statement(t, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t("+definition+") INCLUDE(payload)"), 0, nil)
				})
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui"`, Detail: detail, Schema: "public", Table: "t", Constraint: "ui"})
				initial, _ := concurrentUnitIndex(t, e, "ui", false, false)
				_, err = e.Execute(owner, enginetest.Statement(t, "REINDEX INDEX ui"), 0, nil)
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui"`, Detail: detail, Schema: "public", Table: "t", Constraint: "ui"})
				before := e.CatalogRead().State()
				_, err = e.Execute(owner, enginetest.Statement(t, "ALTER TABLE t ALTER n TYPE BIGINT USING 0"), 0, nil)
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui"`, Detail: typeDetail, Schema: "public", Table: "t", Constraint: "ui"})
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
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "ui"`, Detail: writeDetail, Schema: "public", Table: "t", Constraint: "ui"})
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

// withPublicSchemaUnitOwner provisions only a positive CREATE or owner-successor
// precondition. It leaves the tested operation's identity and object ACLs alone.
func withPublicSchemaUnitOwner(t *testing.T, e *core.Engine, owner string, body func()) {
	t.Helper()
	view := e.CatalogRead()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	if len(addresses) != 1 {
		t.Fatal("public schema fixture requires one public namespace")
	}
	descriptor, ok := view.Descriptor(addresses[0])
	if !ok {
		t.Fatal("public schema fixture requires its namespace descriptor")
	}
	namespace, ok := descriptor.(*catalog.Namespace)
	if !ok {
		t.Fatal("public schema fixture has a non-namespace descriptor")
	}
	roles := e.Roles()
	prior, err := roles.Descriptor(namespace.Owner)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := roles.Descriptor(e.RoleStore.Domain.Principal(10))
	if err != nil {
		t.Fatal(err)
	}
	login, err := e.ResolveLogin(bootstrap.Name)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))}, login)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		sess.RequestEnd(session.CleanClose)
		if err := sess.WaitEnd(context.Background()); err != nil {
			t.Error("closing public schema fixture session:", err)
		}
	}()
	admin, err := sess.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Done()
	quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
	// Restore while the borrowed operation and the prior owner are still live,
	// including when the body terminates through testing.T.Fatal.
	defer func() {
		restore, err := parser.Parse("ALTER SCHEMA public OWNER TO " + quote(prior.Name))
		if err != nil {
			t.Error("parsing public schema owner restoration:", err)
			return
		}
		if len(restore) != 1 {
			t.Errorf("expected single public schema owner restoration statement, got %d", len(restore))
			return
		}
		if _, err := e.Execute(admin, restore[0], 0, nil); err != nil {
			t.Error("restoring public schema owner:", err)
		}
	}()
	enginetest.RoleExecute(t, e, admin, "ALTER SCHEMA public OWNER TO "+quote(owner))
	body()
}

// testSessionID numbers the sessions this package's tests start directly. It
// starts above the IDs the enginetest helpers mint, so a test that uses both
// never registers a duplicate advisory session.
var testSessionID atomic.Uint64

func init() { testSessionID.Store(1 << 20) }
