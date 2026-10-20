package engine

import (
	"context"
	"errors"
	"os"
	osexec "os/exec"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

// Run the actual callback replacement executor in an isolated test process:
// the pre-remedy mutex deadlock cannot strand the parent suite or its cleanup.
func TestTypedPublicationCallbackReplacement(t *testing.T) {
	if kind := os.Getenv("STUNTGRES_TYPED_CALLBACK_CASE"); kind != "" {
		typedCallbackReplacement(t, kind)
		return
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"alter", "drop", "create", "temporary", "attachment"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := osexec.CommandContext(ctx, binary, "-test.run=^TestTypedPublicationCallbackReplacement$", "-test.count=1", "-test.timeout=8s")
			cmd.Env = append(os.Environ(), "STUNTGRES_TYPED_CALLBACK_CASE="+kind)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual %s provider replacement did not complete: %v\n%s", kind, err, output)
			}
		})
	}
}

func typedCallbackReplacement(t *testing.T, kind string) {
	e, source, table := typedUnitCatalog(t, false)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "INSERT INTO typed VALUES(11,'kept')")
	query := "ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE"
	if kind == "drop" {
		// A surviving holder descriptor makes DROP validation callback-capable;
		// dropping only whole typed relations would validate no survivor.
		roleExecute(t, e, op, "CREATE TABLE holder(v pair,k integer)")
		roleExecute(t, e, op, "INSERT INTO holder VALUES('(7,held)',19)")
		query = "DROP TYPE pair CASCADE"
	}
	if kind == "create" {
		query = "CREATE TABLE late OF pair"
	}
	if kind == "temporary" {
		query = "CREATE TEMP TABLE late OF pair"
	}
	if kind == "attachment" {
		query = "ALTER TABLE typed OF pair"
	}
	epoch := e.CatalogRead().Epoch()
	original := e.catalog.Providers[ids.RelationClassID]
	called := false
	e.catalog.Providers[ids.RelationClassID] = alterDropProvider{original, func() {
		if called {
			return
		}
		called = true
		e.catalog.Providers[ids.RelationClassID] = original
		if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE sentinel(a integer);INSERT INTO sentinel VALUES(91)")); err != nil {
			t.Fatal(err)
		}
	}}
	_, err := e.Execute(op, statement(t, query), 0, nil)
	if !called || !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("callback did not reject captured epoch", called, err)
	}
	view := e.CatalogRead()
	if view.Epoch() == epoch || view.Get(source.Address()) == source || view.Get(table.Address()) == table {
		t.Fatal("stale typed state resurrected")
	}
	for _, name := range []string{"pair", "typed", "holder"} {
		if _, ok := view.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: source.Namespace(), Name: name}); ok {
			t.Fatal("replacement resurrected old relation", name)
		}
	}
	result := roleExecute(t, e, op, "SELECT a FROM sentinel")
	if values := tuples(result); len(values) != 1 || values[0][0] != int64(91) {
		t.Fatal(values)
	}
	if result.Rows != nil {
		if err := result.Rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	lockEmpty(t, e)
	if !e.replacementMu.TryLock() {
		t.Fatal("replacement read reservation leaked")
	}
	e.replacementMu.Unlock()
}

func TestTypedAdmissionCreateSourceGrant(t *testing.T) {
	for _, temporary := range []bool{false, true} {
		t.Run(map[bool]string{false: "persistent", true: "temporary"}[temporary], func(t *testing.T) {
			e, source, _ := typedUnitCatalog(t, false)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			query := "CREATE TABLE late OF pair"
			if temporary {
				query = "CREATE TEMP TABLE late OF pair"
			}
			if _, err := e.Describe(op, statement(t, query)); err != nil {
				t.Fatal(err)
			}
			lockEmpty(t, e)
			original := e.catalog.Providers[ids.RelationClassID]
			called, granted := false, false
			e.catalog.Providers[ids.RelationClassID] = alterDropProvider{original, func() {
				called = true
				ref := e.catalog.Domain.Ref(e.CatalogRead().Epoch(), source.Address())
				lockState := e.locks.Inspect()
				if entry := lockState.Resources[lock.Resource{ObjectRef: ref}]; entry != nil {
					for _, holder := range entry.Holders {
						granted = granted || holder.Mode == lock.AccessShare
					}
				}
			}}
			defer func() { e.catalog.Providers[ids.RelationClassID] = original }()
			roleExecute(t, e, op, query)
			if !called || !granted {
				t.Fatal("actual CREATE validation lacks retained source AccessShare grant", called, granted)
			}
			lockEmpty(t, e)
		})
	}
}

func TestTypedPublicationLateOwnRowConsumer(t *testing.T) {
	for _, rowName := range []string{"typed", "child"} {
		t.Run(rowName, func(t *testing.T) {
			e, source, root := typedUnitCatalog(t, false)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			predecessor, before := root, e.CatalogRead()
			stored := e.storage.Relations[root.Address()]
			roleExecute(t, e, op, "CREATE TABLE child() INHERITS(typed)")
			root = e.CatalogRead().MustRelation(predecessor.Address())
			if root == predecessor || !root.History.HasSubclasses || predecessor.History.HasSubclasses || root.Address() != predecessor.Address() || root.RowType != predecessor.RowType || root.OfType != predecessor.OfType || root.Revision() == predecessor.Revision() || before.Get(predecessor.Address()) != predecessor || e.storage.Relations[root.Address()] != stored {
				t.Fatal("successful child publication did not preserve immutable typed identity/storage and transition subclass history")
			}
			roleExecute(t, e, op, "INSERT INTO typed VALUES(11,'root')")
			roleExecute(t, e, op, "INSERT INTO child VALUES(7,'leaf')")
			view := e.CatalogRead()
			childRef := roleTableRef(t, e, "child")
			child := view.MustRelation(childRef.Address)
			rootStorage, childStorage := e.storage.Relations[root.Address()], e.storage.Relations[child.Address()]
			original := e.catalog.Providers[ids.RelationClassID]
			called := false
			var after *catalog.State
			e.catalog.Providers[ids.RelationClassID] = alterDropProvider{original, func() {
				if called {
					return
				}
				called = true
				e.catalog.Providers[ids.RelationClassID] = original
				creator, finish := roleOperation(t, e, "postgres")
				defer finish()
				roleExecute(t, e, creator, "CREATE TABLE holder(v "+rowName+")")
				after = e.CatalogRead().State()
			}}
			_, err := e.Execute(op, statement(t, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE integer CASCADE"), 0, nil)
			var conflict catalog.Conflict
			if !called || !errors.As(err, &conflict) {
				t.Fatal("late semantic own-row dependency admitted", called, err)
			}
			current := e.CatalogRead()
			if current.State() != after || current.Get(source.Address()) != source || current.Get(root.Address()) != root || current.Get(child.Address()) != child || e.storage.Relations[root.Address()] != rootStorage || e.storage.Relations[child.Address()] != childStorage {
				t.Fatal("rejected TYPE changed published graph or storage")
			}
			if root.OfType != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)}) {
				t.Fatal("attachment changed")
			}
			for _, relation := range []*catalog.Relation{source, root, child} {
				if relation.Columns[0].Usage.Type.ID() != sqltype.Int4 || relation.Columns[0].Name != "id" {
					t.Fatal("semantic conflict changed shape", relation)
				}
			}
			holderRef := roleTableRef(t, e, "holder")
			holder := current.MustRelation(holderRef.Address)
			row := root.RowType
			if rowName == "child" {
				row = child.RowType
			}
			if holder.Columns[0].Usage.Type.ID() != row {
				t.Fatal("holder does not use actual own-row provider")
			}
			for _, expected := range []struct {
				store *storage.Relation
				id    int64
				note  string
			}{{rootStorage, 11, "root"}, {childStorage, 7, "leaf"}} {
				if len(expected.store.Versions) != 1 || len(expected.store.Versions[0].Values) != 2 || testExport(expected.store.Versions[0].Values[0]) != expected.id || testExport(expected.store.Versions[0].Values[1]) != expected.note {
					t.Fatal("rejected TYPE changed independently expected rows")
				}
			}
			lockEmpty(t, e)
		})
	}
}

// Publish real immutable type ACL descriptors, not a replacement SQL codec or
// role/session revision. Type-only ACL replacement is the established seam.
func typedReviewTypeACL(t *testing.T, e *Engine, address ids.ObjectAddress, acl authorization.ACLValue) {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	next := *stage.Base.MustType(address)
	next.ACL, next.ObjectRevision = acl, next.ObjectRevision+1
	next.ACLSyntheticDefault = false
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
}

func typedReviewACLFixture(t *testing.T) (*Engine, *Operation, func(), ids.ObjectAddress) {
	t.Helper()
	e := New()
	admin, finish := roleOperation(t, e, "postgres")
	roleExecute(t, e, admin, "CREATE USER typed_owner")
	roleExecute(t, e, admin, "CREATE USER typed_reader")
	roleExecute(t, e, admin, "CREATE DOMAIN member AS integer")
	roleExecute(t, e, admin, "CREATE TYPE foreign_pair AS (z integer)")
	ns := e.catalog.Domain.Ref(e.CatalogRead().Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	if err := e.SetObjectACL(context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: admin.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	finish()
	op, done := roleOperation(t, e, "typed_owner")
	roleExecute(t, e, op, "CREATE TYPE pair AS (id integer)")
	roleExecute(t, e, op, "CREATE TABLE typed OF pair")
	address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ns.Address, Name: "member"})
	return e, op, done, address
}

func TestTypedPublicationMemberACLConflict(t *testing.T) {
	e, op, done, member := typedReviewACLFixture(t)
	defer done()
	before := e.CatalogRead()
	originalMember := before.MustType(member)
	pair := roleTableRef(t, e, "pair")
	table := roleTableRef(t, e, "typed")
	oldSource, oldTable, oldStorage := before.Get(pair.Address), before.Get(table.Address), e.storage.Relations[table.Address]
	original := e.catalog.Providers[ids.RelationClassID]
	called := false
	var after *catalog.State
	e.catalog.Providers[ids.RelationClassID] = alterDropProvider{original, func() {
		if called {
			return
		}
		called = true
		e.catalog.Providers[ids.RelationClassID] = original
		typedReviewTypeACL(t, e, member, authorization.ACLValue{Explicit: true})
		after = e.CatalogRead().State()
	}}
	_, err := e.Execute(op, statement(t, "ALTER TYPE pair ADD ATTRIBUTE value member CASCADE"), 0, nil)
	var conflict catalog.Conflict
	if !called || !errors.As(err, &conflict) {
		t.Fatal("late member ACL change escaped final authority fence", called, err)
	}
	current := e.CatalogRead()
	changed := current.MustType(member)
	if current.State() != after || current.Get(pair.Address) != oldSource || current.Get(table.Address) != oldTable || e.storage.Relations[table.Address] != oldStorage || changed.SQLType != originalMember.SQLType || reflect.DeepEqual(changed.ACL, originalMember.ACL) {
		t.Fatal("ACL conflict was not type-descriptor-only and atomic")
	}
	lockEmpty(t, e)
}
