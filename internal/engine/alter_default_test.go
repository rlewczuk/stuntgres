package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAlterDefaultDescriptor(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(gone INT,a INT DEFAULT 11,b INT DEFAULT 13)")
	run(t, e, "INSERT INTO t VALUES(0,11,13)")
	run(t, e, "ALTER TABLE t DROP gone")
	run(t, e, "ALTER TABLE t RENAME a TO renamed")
	ref := roleTableRef(t, e, "t")
	read := e.CatalogRead()
	old := read.MustRelation(ref.Address)
	storage := e.Storage.Relations[ref.Address]
	ends, generation := storage.Ends, storage.Generation
	oldA, oldB := old.ColumnMeta.Defaults[old.Attributes[0]], old.ColumnMeta.Defaults[old.Attributes[1]]
	run(t, e, "ALTER TABLE t ALTER renamed SET DEFAULT 7")
	next := e.Catalog.State.MustRelation(ref.Address)
	if next == old || next.Revision() != old.Revision()+1 || next.Address() != old.Address() || !reflect.DeepEqual(next.Attributes, old.Attributes) || &next.Columns[0] != &old.Columns[0] || next.ColumnMeta.Defaults[old.Attributes[1]].Expression != oldB.Expression || old.ColumnMeta.Defaults[old.Attributes[0]].Expression != oldA.Expression || read.Get(ref.Address) != old {
		t.Fatal("descriptor immutability/stable attributes violated")
	}
	if e.Storage.Relations[ref.Address] != storage || storage.Ends != ends || storage.Generation != generation || len(storage.Versions) != 1 {
		t.Fatal("metadata alteration rewrote storage")
	}
	run(t, e, "INSERT INTO t DEFAULT VALUES")
	got := tuples(run(t, e, "SELECT * FROM t ORDER BY renamed"))
	if len(got) != 2 || got[0][0] != int64(7) || got[0][1] != int64(13) || got[1][0] != int64(11) || got[1][1] != int64(13) {
		t.Fatal(got)
	}
	run(t, e, "ALTER TABLE t ALTER renamed DROP DEFAULT")
	d := e.Catalog.State.MustRelation(ref.Address)
	if _, ok := d.ColumnMeta.Defaults[old.Attributes[0]]; ok {
		t.Fatal("default retained")
	}
	if d.ColumnMeta.Defaults[old.Attributes[1]].Expression != oldB.Expression {
		t.Fatal("other default changed")
	}
	run(t, e, "ALTER TABLE t ALTER renamed DROP DEFAULT")
	if e.Catalog.State.Get(ref.Address) != d {
		t.Fatal("absent DROP allocated revision")
	}
	e.Mu.Lock()
	exhausted := *d
	exhausted.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	e.Catalog.State.SetDescriptor(ref.Address, &exhausted)
	e.Mu.Unlock()
	_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE t ALTER renamed SET DEFAULT 9"), nil)
	if err == nil || e.Catalog.State.Get(ref.Address) != &exhausted {
		t.Fatal("revision exhaustion mutated", err)
	}
	run(t, e, "ALTER TABLE t ALTER renamed DROP DEFAULT")
}

func TestAlterDefaultLifecycleFences(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t ALTER a SET DEFAULT 7", "ALTER TABLE t ALTER a DROP DEFAULT"} {
		for _, ending := range []bool{false, true} {
			t.Run(q+map[bool]string{false: "/cancel", true: "/end"}[ending], func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE TABLE t(a INT DEFAULT 11,b INT DEFAULT 13)")
				sessionSQL(t, a, "INSERT INTO t DEFAULT VALUES")
				ref := roleTableRef(t, e, "t")
				old, storage := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
				ends, generation := storage.Ends, storage.Generation
				tx := transactionBegin(t, a)
				sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := lockAsync(e, b, ctx, q, false)
				lockPending(t, e, 1)
				if ending {
					b.RequestEnd(session.CleanClose)
				} else {
					cancel()
				}
				lockAwait(t, done, context.Canceled)
				lockPending(t, e, 0)
				if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || storage.Ends != ends || storage.Generation != generation || len(storage.Versions) != 1 {
					t.Fatal("canceled alteration changed descriptor/storage")
				}
				transactionEnd(t, a, tx, false)
				lockEmpty(t, e)
				run(t, e, "INSERT INTO t DEFAULT VALUES")
				got := tuples(run(t, e, "SELECT * FROM t"))
				if len(got) != 2 || got[1][0] != int64(11) || got[1][1] != int64(13) {
					t.Fatal(got)
				}
			})
		}
	}
	for _, kind := range []string{"cancel", "reset", "restore", "role", "auth", "end"} {
		t.Run("provider/"+kind, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(a INT DEFAULT 11)")
			ref := roleTableRef(t, e, "t")
			old := e.Catalog.State.Get(ref.Address)
			storage := e.Storage.Relations[ref.Address]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := e.Catalog.Providers[ids.RelationClassID]
			op, cleanup, err := testOperation(e, ctx)
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
					if err := Reset(context.Background(), e); err != nil {
						t.Fatal(err)
					}
				case "restore":
					if err := Restore(context.Background(), e, strings.NewReader("CREATE TABLE sentinel(i INT)")); err != nil {
						t.Fatal(err)
					}
				case "role":
					run(t, e, "CREATE USER changed")
				case "auth":
					op.Session.Mu.Lock()
					op.Session.Authorization.Revision++
					op.Session.Mu.Unlock()
				case "end":
					op.Session.RequestEnd(session.CleanClose)
				}
			}}
			_, err = e.Execute(op, statement(t, "ALTER TABLE t ALTER a SET DEFAULT 7"), 0, nil)
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
			lockEmpty(t, e)
		})
	}
}

func TestAlterDefaultOwnerAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "CREATE TABLE t(a INT DEFAULT 11)")
	ref := roleTableRef(t, e, "t")
	reader, _ := e.Roles().Lookup("reader")
	owner := op.RoleState().CurrentUser
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	old := e.Catalog.State.Get(ref.Address)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	for _, action := range []string{"SET DEFAULT 'oops'", "DROP DEFAULT"} {
		_, err := e.Execute(op, statement(t, "ALTER TABLE t ALTER a "+action), 0, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table t"}
		if !errors.As(err, &got) || *got != want || e.Catalog.State.Get(ref.Address) != old {
			t.Fatal(err, got)
		}
	}
	withPublicSchemaUnitOwner(t, e, "reader", func() {
		roleExecute(t, e, op, "CREATE TABLE owned(a INT)")
	})
	roleExecute(t, e, op, "ALTER TABLE owned ALTER a SET DEFAULT 7")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "ALTER TABLE t ALTER a SET DEFAULT 7")
}
