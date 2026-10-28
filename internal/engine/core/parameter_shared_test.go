package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func parameterSharedUnitFixture(t *testing.T) (*core.Engine, *core.Engine) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE ROLE grant_reader LOGIN")
	enginetest.SessionSQL(t, s, "CREATE DATABASE parameter_peer")
	b, err := e.SelectDatabase(context.Background(), "parameter_peer")
	if err != nil { t.Fatal(err) }
	return e, b
}

func parameterSharedUnitGrant(t *testing.T, e *core.Engine, name string, revoke bool) {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	request := parameterSpaceRequest("parameter", name)
	request.Revoke = revoke
	if err := applyObjectGrantUnit(e, op, request); err != nil { t.Fatal(err) }
}

func parameterSharedUnitDescriptor(e *core.Engine, name string) *catalog.Parameter {
	address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: name})
	parameter, _ := e.Catalog.State.Get(address).(*catalog.Parameter)
	return parameter
}

func parameterSharedUnitCoherent(t *testing.T, name string, engines ...*core.Engine) *catalog.Parameter {
	t.Helper()
	first := parameterSharedUnitDescriptor(engines[0], name)
	for _, e := range engines[1:] {
		if parameterSharedUnitDescriptor(e, name) != first { t.Fatal("shared descriptor backing diverged", name) }
		if e.Catalog.State.ParameterIncarnation() != engines[0].Catalog.State.ParameterIncarnation() { t.Fatal("shared incarnation diverged") }
	}
	return first
}

func TestParameterACLSharedProjectionSnapshots(t *testing.T) {
	a, b := parameterSharedUnitFixture(t)
	parameterSharedUnitGrant(t, a, "shared_unit.snapshot", false)
	old := parameterSharedUnitCoherent(t, "shared_unit.snapshot", a, b)
	if old == nil || old.Address().ObjectID == 0 { t.Fatal("missing shared identity") }
	rootA, rootB, roles := a.Catalog.State, b.Catalog.State, a.RoleStore.Published
	entries := append([]authorization.ACLEntry(nil), old.ACL.Entries...)
	backing := &old.ACL.Entries[0]
	local := catalog.CloneState(rootA)
	namespace := *rootA.Get(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}).(*catalog.Namespace)
	namespace.ObjectRevision++
	local.SetDescriptor(namespace.Address(), &namespace)
	for _, candidate := range [3]*catalog.State{rootA, catalog.CloneState(rootA), local} {
		if allocations := testing.AllocsPerRun(10, func() {
			a.Mu.Lock()
			projection := a.Catalog.ProjectSharedLocked(candidate, a.Cluster.Catalogs)
			a.Mu.Unlock()
			if projection != nil { t.Fatal("unchanged shared projection") }
		}); allocations != 0 { t.Fatal("unchanged shared projection allocated", allocations) }
	}
	parameterSharedUnitGrant(t, b, "shared_unit.snapshot", true)
	if parameterSharedUnitCoherent(t, "shared_unit.snapshot", a, b) != nil { t.Fatal("sparse deletion not projected") }
	key := catalog.DescriptorNameKey(old)
	if rootA.Get(old.Address()) != old || rootB.Get(old.Address()) != old || rootA.NameAddress(key) != old.Address() || rootB.NameAddress(key) != old.Address() || &old.ACL.Entries[0] != backing || !reflect.DeepEqual(old.ACL.Entries, entries) { t.Fatal("retained descriptor/name/ACL backing mutated") }
	if roles == a.RoleStore.Published { t.Fatal("missing distinguishing role publication") }
	parameterSharedUnitGrant(t, a, "shared_unit.snapshot", false)
	current := parameterSharedUnitCoherent(t, "shared_unit.snapshot", a, b)
	if current == old || current.Address() == old.Address() { t.Fatal("recreation retained retired identity") }
	s := enginetest.StartSession(t, a, nil)
	enginetest.SessionSQL(t, s, "CREATE DATABASE parameter_later")
	c, err := a.SelectDatabase(context.Background(), "parameter_later")
	if err != nil { t.Fatal(err) }
	parameterSharedUnitCoherent(t, "shared_unit.snapshot", a, b, c)
	if a.Catalog.Domain == b.Catalog.Domain || b.Catalog.Domain == c.Catalog.Domain { t.Fatal("projection shared local domains") }
	// Explicit empty is a real descriptor, unlike an absent default baseline.
	next := *current
	next.ACL = authorization.ACLValue{Explicit: true}
	if err := enginetest.SetObjectACL(a, context.Background(), a.Catalog.Domain.Ref(a.Catalog.State.Epoch(), current.Address()), next.ACL); err != nil { t.Fatal(err) }
	empty := parameterSharedUnitCoherent(t, "shared_unit.snapshot", a, b, c)
	if empty == nil || !empty.ACL.Explicit || len(empty.ACL.Entries) != 0 || empty.Address() != current.Address() { t.Fatal("explicit empty collapsed") }
	if !reflect.DeepEqual(old.ACL.Entries, entries) || roles.Root.Read(authorization.PrincipalRef{}).LookupRef("grant_reader").ID() == 0 { t.Fatal("old coherent authority damaged") }
}

func TestParameterACLSharedPublicationFences(t *testing.T) {
	a, b := parameterSharedUnitFixture(t)
	sa, sb := enginetest.StartSession(t, a, nil), enginetest.StartSession(t, b, nil)
	enginetest.SessionSQL(t, sb, "BEGIN")
	enginetest.SessionSQL(t, sb, "CREATE TABLE shared_local(id INT)")
	journal := sb.Transactions.Catalog
	oldRoot := b.Catalog.State
	parameterSharedUnitGrant(t, a, "shared_unit.external", false)
	peer := parameterSharedUnitDescriptor(a, "shared_unit.external")
	enginetest.SessionSQL(t, sb, "COMMIT")
	if parameterSharedUnitCoherent(t, "shared_unit.external", a, b) != peer || journal == nil || oldRoot == b.Catalog.State { t.Fatal("stale local journal overwrote shared authority") }
	enginetest.SessionSQL(t, sa, "BEGIN")
	enginetest.SessionSQL(t, sb, "BEGIN")
	enginetest.SessionSQL(t, sa, "GRANT SET ON PARAMETER shared_unit.left_value TO grant_reader")
	enginetest.SessionSQL(t, sb, "GRANT SET ON PARAMETER shared_unit.right_value TO grant_reader")
	left, right := sa.Transactions.Catalog, sb.Transactions.Catalog
	leftRoot := left.PrivateRead(&a.Catalog.Owner, a.Catalog.State).State()
	enginetest.SessionSQL(t, sb, "COMMIT")
	enginetest.SessionSQL(t, sa, "SAVEPOINT prior")
	enginetest.SessionSQL(t, sa, "REVOKE SET ON PARAMETER shared_unit.left_value FROM grant_reader")
	enginetest.SessionSQL(t, sa, "ROLLBACK TO prior")
	if sa.Transactions.Catalog != left || !left.ParameterWritten || left.ParameterIncarnation != right.ParameterIncarnation { t.Fatal("savepoint lost exact original parameter journal") }
	enginetest.SessionSQL(t, sa, "COMMIT")
	for _, name := range []string{"shared_unit.left_value", "shared_unit.right_value", "shared_unit.external"} {
		if parameterSharedUnitCoherent(t, name, a, b) == nil { t.Fatal("lost disjoint publication", name) }
	}
	if leftRoot.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: "shared_unit.right_value"}) != (ids.ObjectAddress{}) { t.Fatal("retained private root changed after peer commit") }
}

// Capture an actual statement journal before Finish; this is not a copied SQL
// result. Its admission and immutable predecessor survive until final publication.
func parameterSharedUnitPending(t *testing.T, e *core.Engine, name string) (*core.StatementScope, *session.Operation, func()) {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	scope, err := e.StartStatement(op)
	if err != nil { done(); t.Fatal(err) }
	_, err = scope.Execute(parameterSpaceRequest("parameter", name), 0, nil)
	if err != nil { scope.Finish(err); done(); t.Fatal(err) }
	if scope.PendingCatalog() == nil || !scope.PendingCatalog().ParameterWritten { scope.Finish(context.Canceled); done(); t.Fatal("missing real pending parameter journal") }
	return scope, op, done
}
