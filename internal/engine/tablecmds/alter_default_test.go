package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestAlterDefaultDescriptor(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(gone INT,a INT DEFAULT 11,b INT DEFAULT 13)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(0,11,13)")
	enginetest.Run(t, e, "ALTER TABLE t DROP gone")
	enginetest.Run(t, e, "ALTER TABLE t RENAME a TO renamed")
	ref := enginetest.RoleTableRef(t, e, "t")
	read := e.CatalogRead()
	old := read.MustRelation(ref.Address)
	storage := e.Storage.Relations[ref.Address]
	ends, generation := storage.Ends, storage.Generation
	oldA, oldB := old.ColumnMeta.Defaults[old.Attributes[0]], old.ColumnMeta.Defaults[old.Attributes[1]]
	enginetest.Run(t, e, "ALTER TABLE t ALTER renamed SET DEFAULT 7")
	next := e.Catalog.State.MustRelation(ref.Address)
	if next == old || next.Revision() != old.Revision()+1 || next.Address() != old.Address() || !reflect.DeepEqual(next.Attributes, old.Attributes) || &next.Columns[0] != &old.Columns[0] || next.ColumnMeta.Defaults[old.Attributes[1]].Expression != oldB.Expression || old.ColumnMeta.Defaults[old.Attributes[0]].Expression != oldA.Expression || read.Get(ref.Address) != old {
		t.Fatal("descriptor immutability/stable attributes violated")
	}
	if e.Storage.Relations[ref.Address] != storage || storage.Ends != ends || storage.Generation != generation || len(storage.Versions) != 1 {
		t.Fatal("metadata alteration rewrote storage")
	}
	enginetest.Run(t, e, "INSERT INTO t DEFAULT VALUES")
	got := enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM t ORDER BY renamed"))
	if len(got) != 2 || got[0][0] != int64(7) || got[0][1] != int64(13) || got[1][0] != int64(11) || got[1][1] != int64(13) {
		t.Fatal(got)
	}
	enginetest.Run(t, e, "ALTER TABLE t ALTER renamed DROP DEFAULT")
	d := e.Catalog.State.MustRelation(ref.Address)
	if _, ok := d.ColumnMeta.Defaults[old.Attributes[0]]; ok {
		t.Fatal("default retained")
	}
	if d.ColumnMeta.Defaults[old.Attributes[1]].Expression != oldB.Expression {
		t.Fatal("other default changed")
	}
	enginetest.Run(t, e, "ALTER TABLE t ALTER renamed DROP DEFAULT")
	if e.Catalog.State.Get(ref.Address) != d {
		t.Fatal("absent DROP allocated revision")
	}
	e.Mu.Lock()
	exhausted := *d
	exhausted.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	e.Catalog.State.SetDescriptor(ref.Address, &exhausted)
	e.Mu.Unlock()
	_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "ALTER TABLE t ALTER renamed SET DEFAULT 9"), nil)
	if err == nil || e.Catalog.State.Get(ref.Address) != &exhausted {
		t.Fatal("revision exhaustion mutated", err)
	}
	enginetest.Run(t, e, "ALTER TABLE t ALTER renamed DROP DEFAULT")
}

func TestAlterDefaultLifecycleFences(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t ALTER a SET DEFAULT 7", "ALTER TABLE t ALTER a DROP DEFAULT"} {
		for _, ending := range []bool{false, true} {
			t.Run(q+map[bool]string{false: "/cancel", true: "/end"}[ending], func(t *testing.T) {
				e := engine.New()
				a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT DEFAULT 11,b INT DEFAULT 13)")
				enginetest.SessionSQL(t, a, "INSERT INTO t DEFAULT VALUES")
				ref := enginetest.RoleTableRef(t, e, "t")
				old, storage := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
				ends, generation := storage.Ends, storage.Generation
				tx := enginetest.TransactionBegin(t, a)
				enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := enginetest.LockAsync(e, b, ctx, q, false)
				enginetest.LockPending(t, e, 1)
				if ending {
					b.RequestEnd(session.CleanClose)
				} else {
					cancel()
				}
				enginetest.LockAwait(t, done, context.Canceled)
				enginetest.LockPending(t, e, 0)
				if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || storage.Ends != ends || storage.Generation != generation || len(storage.Versions) != 1 {
					t.Fatal("canceled alteration changed descriptor/storage")
				}
				enginetest.TransactionEnd(t, a, tx, false)
				enginetest.LockEmpty(t, e)
				enginetest.Run(t, e, "INSERT INTO t DEFAULT VALUES")
				got := enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM t"))
				if len(got) != 2 || got[1][0] != int64(11) || got[1][1] != int64(13) {
					t.Fatal(got)
				}
			})
		}
	}
	for _, kind := range []string{"cancel", "reset", "restore", "role", "auth", "end"} {
		t.Run("provider/"+kind, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE t(a INT DEFAULT 11)")
			ref := enginetest.RoleTableRef(t, e, "t")
			old := e.Catalog.State.Get(ref.Address)
			storage := e.Storage.Relations[ref.Address]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := e.Catalog.Providers[ids.RelationClassID]
			op, cleanup, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{provider, func() {
				e.Catalog.Providers[ids.RelationClassID] = provider
				switch kind {
				case "cancel":
					cancel()
				case "reset":
					if err := engine.Reset(context.Background(), e); err != nil {
						t.Fatal(err)
					}
				case "restore":
					if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE sentinel(i INT)")); err != nil {
						t.Fatal(err)
					}
				case "role":
					enginetest.Run(t, e, "CREATE USER changed")
				case "auth":
					op.Session.Mu.Lock()
					op.Session.Authorization.Revision++
					op.Session.Mu.Unlock()
				case "end":
					op.Session.RequestEnd(session.CleanClose)
				}
			}}
			_, err = e.Execute(op, enginetest.Statement(t, "ALTER TABLE t ALTER a SET DEFAULT 7"), 0, nil)
			if err == nil {
				t.Fatal("stale publication succeeded")
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if kind == "reset" || kind == "restore" {
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
			} else if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage {
				t.Fatal("failed publication leaked default", err)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestAlterDefaultOwnerAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER reader")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(a INT DEFAULT 11)")
	ref := enginetest.RoleTableRef(t, e, "t")
	reader, _ := e.Roles().Lookup("reader")
	owner := op.RoleState().CurrentUser
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	old := e.Catalog.State.Get(ref.Address)
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	for _, action := range []string{"SET DEFAULT 'oops'", "DROP DEFAULT"} {
		_, err := e.Execute(op, enginetest.Statement(t, "ALTER TABLE t ALTER a "+action), 0, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table t"}
		if !errors.As(err, &got) || *got != want || e.Catalog.State.Get(ref.Address) != old {
			t.Fatal(err, got)
		}
	}
	withPublicSchemaUnitOwner(t, e, "reader", func() {
		enginetest.RoleExecute(t, e, op, "CREATE TABLE owned(a INT)")
	})
	enginetest.RoleExecute(t, e, op, "ALTER TABLE owned ALTER a SET DEFAULT 7")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	enginetest.RoleExecute(t, e, op, "ALTER TABLE t ALTER a SET DEFAULT 7")
}

// withPublicSchemaUnitOwner provisions only a positive CREATE or owner-successor
// precondition. It leaves the tested operation's identity and object ACLs alone.
func withPublicSchemaUnitOwner(t *testing.T, e *engine.Engine, owner string, body func()) {
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
	sess, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: login})
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

var testSessionID atomic.Uint64
