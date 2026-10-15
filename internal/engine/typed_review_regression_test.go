package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
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
			cmd := exec.CommandContext(ctx, binary, "-test.run=^TestTypedPublicationCallbackReplacement$", "-test.count=1", "-test.timeout=8s")
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
	original := e.providers[RelationClassID]
	called := false
	e.providers[RelationClassID] = alterDropProvider{original, func() {
		if called {
			return
		}
		called = true
		e.providers[RelationClassID] = original
		if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE sentinel(a integer);INSERT INTO sentinel VALUES(91)")); err != nil {
			t.Fatal(err)
		}
	}}
	_, err := e.Execute(op, statement(t, query), 0, nil)
	if !called || !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("callback did not reject captured epoch", called, err)
	}
	view := e.CatalogRead()
	if view.Epoch() == epoch || view.state.descriptors[source.address] == source || view.state.descriptors[table.address] == table {
		t.Fatal("stale typed state resurrected")
	}
	for _, name := range []string{"pair", "typed", "holder"} {
		if _, ok := view.state.names[NameKey{ClassID: RelationClassID, Namespace: source.namespace, Name: name}]; ok {
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
			original := e.providers[RelationClassID]
			called, granted := false, false
			e.providers[RelationClassID] = alterDropProvider{original, func() {
				called = true
				ref := ObjectRef{owner: e.owner, Epoch: e.CatalogRead().Epoch(), Address: source.address}
				e.locks.mu.Lock()
				if entry := e.locks.entries[lockResource{ObjectRef: ref}]; entry != nil {
					for _, holder := range entry.holders {
						granted = granted || holder.mode == accessShare
					}
				}
				e.locks.mu.Unlock()
			}}
			defer func() { e.providers[RelationClassID] = original }()
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
			storage := e.storage[root.address]
			roleExecute(t, e, op, "CREATE TABLE child() INHERITS(typed)")
			root = e.CatalogRead().state.descriptors[predecessor.address].(*relationDescriptor)
			if root == predecessor || !root.hasSubclasses || predecessor.hasSubclasses || root.address != predecessor.address || root.rowType != predecessor.rowType || root.ofType != predecessor.ofType || root.revision == predecessor.revision || before.state.descriptors[predecessor.address] != predecessor || e.storage[root.address] != storage {
				t.Fatal("successful child publication did not preserve immutable typed identity/storage and transition subclass history")
			}
			roleExecute(t, e, op, "INSERT INTO typed VALUES(11,'root')")
			roleExecute(t, e, op, "INSERT INTO child VALUES(7,'leaf')")
			view := e.CatalogRead()
			childRef := roleTableRef(t, e, "child")
			child := view.state.descriptors[childRef.Address].(*relationDescriptor)
			rootStorage, childStorage := e.storage[root.address], e.storage[child.address]
			original := e.providers[RelationClassID]
			called := false
			var after *catalogState
			e.providers[RelationClassID] = alterDropProvider{original, func() {
				if called {
					return
				}
				called = true
				e.providers[RelationClassID] = original
				creator, finish := roleOperation(t, e, "postgres")
				defer finish()
				roleExecute(t, e, creator, "CREATE TABLE holder(v "+rowName+")")
				after = e.CatalogRead().state
			}}
			_, err := e.Execute(op, statement(t, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE integer CASCADE"), 0, nil)
			var conflict CatalogConflict
			if !called || !errors.As(err, &conflict) {
				t.Fatal("late semantic own-row dependency admitted", called, err)
			}
			current := e.CatalogRead()
			if current.state != after || current.state.descriptors[source.address] != source || current.state.descriptors[root.address] != root || current.state.descriptors[child.address] != child || e.storage[root.address] != rootStorage || e.storage[child.address] != childStorage {
				t.Fatal("rejected TYPE changed published graph or storage")
			}
			if root.ofType != (ObjectAddress{ClassID: TypeClassID, ObjectID: OID(source.rowType)}) {
				t.Fatal("attachment changed")
			}
			for _, relation := range []*relationDescriptor{source, root, child} {
				if relation.columns[0].Usage.Type.ID() != sqltype.Int4 || relation.columns[0].Name != "id" {
					t.Fatal("semantic conflict changed shape", relation)
				}
			}
			holderRef := roleTableRef(t, e, "holder")
			holder := current.state.descriptors[holderRef.Address].(*relationDescriptor)
			row := root.rowType
			if rowName == "child" {
				row = child.rowType
			}
			if holder.columns[0].Usage.Type.ID() != row {
				t.Fatal("holder does not use actual own-row provider")
			}
			for _, expected := range []struct {
				store *relationStorage
				id    int64
				note  string
			}{{rootStorage, 11, "root"}, {childStorage, 7, "leaf"}} {
				if len(expected.store.versions) != 1 || len(expected.store.versions[0].values) != 2 || testExport(expected.store.versions[0].values[0]) != expected.id || testExport(expected.store.versions[0].values[1]) != expected.note {
					t.Fatal("rejected TYPE changed independently expected rows")
				}
			}
			lockEmpty(t, e)
		})
	}
}

// Publish real immutable type ACL descriptors, not a replacement SQL codec or
// role/session revision. Type-only ACL replacement is the established seam.
func typedReviewTypeACL(t *testing.T, e *Engine, address ObjectAddress, acl authorization.ACLValue) {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	next := *stage.base.state.descriptors[address].(*typeDescriptor)
	next.acl, next.revision = acl, next.revision+1
	next.aclSyntheticDefault = false
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
}

func typedReviewACLFixture(t *testing.T) (*Engine, *Operation, func(), ObjectAddress) {
	t.Helper()
	e := New()
	admin, finish := roleOperation(t, e, "postgres")
	roleExecute(t, e, admin, "CREATE USER typed_owner")
	roleExecute(t, e, admin, "CREATE USER typed_reader")
	roleExecute(t, e, admin, "CREATE DOMAIN member AS integer")
	roleExecute(t, e, admin, "CREATE TYPE foreign_pair AS (z integer)")
	ns := ObjectRef{owner: e.owner, Epoch: e.CatalogRead().Epoch(), Address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}}
	if err := e.SetObjectACL(context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: admin.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	finish()
	op, done := roleOperation(t, e, "typed_owner")
	roleExecute(t, e, op, "CREATE TYPE pair AS (id integer)")
	roleExecute(t, e, op, "CREATE TABLE typed OF pair")
	address := e.CatalogRead().state.names[NameKey{ClassID: TypeClassID, Namespace: ns.Address, Name: "member"}]
	return e, op, done, address
}

func TestTypedAuthorizationProviderACL(t *testing.T) {
	e, owner, done, member := typedReviewACLFixture(t)
	defer done()
	pair := roleTableRef(t, e, "pair")
	source := e.CatalogRead().state.descriptors[pair.Address].(*relationDescriptor)
	typedReviewTypeACL(t, e, ObjectAddress{ClassID: TypeClassID, ObjectID: OID(source.rowType)}, authorization.ACLValue{Explicit: true})
	reader, finish := roleOperation(t, e, "typed_reader")
	defer finish()
	roleExecute(t, e, reader, "CREATE TABLE mine(id integer)")
	roleExecute(t, e, reader, "INSERT INTO mine VALUES(11)")
	before := e.CatalogRead().state
	roleError(t, e, reader, "CREATE TABLE denied OF pair", "42501", "permission denied for type pair", "")
	if e.CatalogRead().state != before {
		t.Fatal("denied CREATE changed catalog")
	}
	roleExecute(t, e, reader, "ALTER TABLE mine OF pair")
	mine := roleTableRef(t, e, "mine")
	mineDescriptor := e.CatalogRead().state.descriptors[mine.Address].(*relationDescriptor)
	if mineDescriptor.ofType.ObjectID != OID(source.rowType) || mineDescriptor.owner == source.owner {
		t.Fatal("attach did not retain reader ownership")
	}
	roleExecute(t, e, owner, "ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE")
	mineDescriptor = e.CatalogRead().state.descriptors[mine.Address].(*relationDescriptor)
	if len(mineDescriptor.columns) != 2 || mineDescriptor.columns[1].Usage.Type.ID() != sqltype.Bool {
		t.Fatal("source-owner CASCADE failed foreign-owner target")
	}
	typedReviewTypeACL(t, e, member, authorization.ACLValue{Explicit: true})
	before = e.CatalogRead().state
	roleError(t, e, owner, "ALTER TYPE pair ADD ATTRIBUTE value member CASCADE", "42501", "permission denied for type member", "")
	roleError(t, e, owner, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE member CASCADE", "42501", "permission denied for type member", "")
	roleError(t, e, owner, "DROP TYPE pair,foreign_pair CASCADE", "42501", "must be owner of type foreign_pair", "")
	if e.CatalogRead().state != before {
		t.Fatal("denied member/grouped DROP changed graph")
	}
	memberDescriptor := e.CatalogRead().state.descriptors[member].(*typeDescriptor)
	grant := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: memberDescriptor.owner, Grantee: authorization.ACLGrantee{Role: owner.RoleState().CurrentUser}, Privileges: authorization.UsagePrivilege}}}
	typedReviewTypeACL(t, e, member, grant)
	roleExecute(t, e, owner, "ALTER TYPE pair ADD ATTRIBUTE value member CASCADE")
	mineDescriptor = e.CatalogRead().state.descriptors[mine.Address].(*relationDescriptor)
	if len(mineDescriptor.columns) != 3 || mineDescriptor.columns[2].Usage.Type.ID() != sqltype.ID(member.ObjectID) {
		t.Fatal("granted member USAGE not reflected in actual target")
	}
	if store := e.storage[mine.Address]; len(store.versions) != 1 || testExport(store.versions[0].values[0]) != int64(11) || !store.versions[0].values[1].IsNull() || !store.versions[0].values[2].IsNull() {
		t.Fatal("ACL actions changed independently expected target rows")
	}
}

func TestTypedPublicationMemberACLConflict(t *testing.T) {
	e, op, done, member := typedReviewACLFixture(t)
	defer done()
	before := e.CatalogRead()
	originalMember := before.state.descriptors[member].(*typeDescriptor)
	pair := roleTableRef(t, e, "pair")
	table := roleTableRef(t, e, "typed")
	oldSource, oldTable, oldStorage := before.state.descriptors[pair.Address], before.state.descriptors[table.Address], e.storage[table.Address]
	original := e.providers[RelationClassID]
	called := false
	var after *catalogState
	e.providers[RelationClassID] = alterDropProvider{original, func() {
		if called {
			return
		}
		called = true
		e.providers[RelationClassID] = original
		typedReviewTypeACL(t, e, member, authorization.ACLValue{Explicit: true})
		after = e.CatalogRead().state
	}}
	_, err := e.Execute(op, statement(t, "ALTER TYPE pair ADD ATTRIBUTE value member CASCADE"), 0, nil)
	var conflict CatalogConflict
	if !called || !errors.As(err, &conflict) {
		t.Fatal("late member ACL change escaped final authority fence", called, err)
	}
	current := e.CatalogRead()
	changed := current.state.descriptors[member].(*typeDescriptor)
	if current.state != after || current.state.descriptors[pair.Address] != oldSource || current.state.descriptors[table.Address] != oldTable || e.storage[table.Address] != oldStorage || changed.typ != originalMember.typ || reflect.DeepEqual(changed.acl, originalMember.acl) {
		t.Fatal("ACL conflict was not type-descriptor-only and atomic")
	}
	lockEmpty(t, e)
}
