package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
)

func TestParameterACLSharedAllocationAdmission(t *testing.T) {
	t.Run("retained-address", parameterSharedRetainedAddress)
	for _, existing := range []bool{false, true} {
		for _, outcome := range []string{"commit", "rollback", "cancel"} {
			t.Run(map[bool]string{false: "first", true: "existing"}[existing]+"/"+outcome, func(t *testing.T) {
				a, b := parameterSharedUnitFixture(t)
				name := "shared_unit.contended"
				if existing {
					setup := enginetest.StartSession(t, a, nil)
					enginetest.SessionSQL(t, setup, "GRANT SET ON PARAMETER shared_unit.contended TO grant_reader")
				}
				holder, waiter := enginetest.StartSession(t, a, nil), enginetest.StartSession(t, b, nil)
				enginetest.SessionSQL(t, holder, "BEGIN")
				enginetest.SessionSQL(t, holder, "GRANT ALL ON PARAMETER shared_unit.contended TO grant_reader WITH GRANT OPTION")
				candidate := holder.Transactions.Catalog
				ctx, cancel := context.WithCancel(context.Background())
				op, err := waiter.Acquire(ctx)
				if err != nil { t.Fatal(err) }
				finished := make(chan error, 1)
				request := parameterSpaceRequest("parameter", name)
				go func() { _, err := b.Execute(op, request, 0, nil); op.Done(); finished <- err }()
				joined := false
				defer func() {
					cancel()
					if !joined { <-finished }
					enginetest.SessionSQL(t, holder, "ROLLBACK")
				}()
				enginetest.LockPending(t, a, 1)
				resource := lock.Resource{ObjectRef: a.Cluster.RoleDomain.Ref(1, ids.ObjectAddress{}), Name: ids.NameKey{ClassID: ids.ParameterClassID, Name: name}}
				entry := a.Locks.Inspect().Resources[resource]
				if entry == nil || len(entry.Pending) != 1 || entry.Pending[0].Terminal || len(entry.Holders) != 1 { t.Fatal("missing actual canonical shared writer queue", entry) }
				pids, err := a.Locks.BlockingPids(b.Catalog.Domain, waiter.SessionToken.ID())
				if err != nil || !reflect.DeepEqual(pids, []int32{int32(holder.SessionToken.ID())}) { t.Fatal("cross-database blocker projection", pids, err) }
				if outcome == "cancel" { cancel() } else { enginetest.SessionSQL(t, holder, map[string]string{"commit": "COMMIT", "rollback": "ROLLBACK"}[outcome]) }
				err = <-finished
				joined = true
				switch outcome {
				case "cancel":
					if !errors.Is(err, context.Canceled) { t.Fatal("waiter cancel", err) }
				case "rollback":
					if err != nil { t.Fatal("holder rollback should leave original authority", err) }
				case "commit":
					want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: "tuple concurrently updated"}
					if !existing { want = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_parameter_acl_parname_index"`, Detail: "Key (parname)=(shared_unit.contended) already exists.", Schema: "pg_catalog", Table: "pg_parameter_acl", Constraint: "pg_parameter_acl_parname_index"} }
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) { t.Fatal("original prewait authority was retargeted", err, want) }
				}
				if candidate == nil || candidate.Descriptors == nil { t.Fatal("holder journal disappeared") }
				parameterSharedUnitCoherent(t, name, a, b)
			})
		}
	}
	t.Run("frontiers", func(t *testing.T) {
		a, b := parameterSharedUnitFixture(t)
		b.Mu.Lock()
		reserved, err := b.Catalog.AllocateOIDLocked()
		b.Mu.Unlock()
		if err != nil { t.Fatal(err) }
		parameterSharedUnitGrant(t, a, "shared_unit.allocated", false)
		parameter := parameterSharedUnitCoherent(t, "shared_unit.allocated", a, b)
		if parameter.Address().ObjectID <= reserved || a.Catalog.NextOID() != b.Catalog.NextOID() { t.Fatal("peer private allocation collided") }
		// Imported identities may be ahead of the ordinary frontier. Install a
		// genuine shared descriptor there, then force the exact collision.
		a.Mu.Lock()
		imported := *parameter
		imported.ObjectAddress.ObjectID = b.Catalog.NextOID() + 100
		imported.ObjectName = "shared_unit.imported"
		state := catalog.CloneState(b.Catalog.State)
		state.SetDescriptor(imported.Address(), &imported)
		state.BindName(catalog.DescriptorNameKey(&imported), imported.Address())
		roots := b.Catalog.ProjectSharedLocked(state, b.Cluster.Catalogs)
		b.Catalog.PublishLocked(state)
		roots.InstallLocked()
		b.Catalog.InstallOIDFrontierLocked(imported.Address().ObjectID)
		a.Mu.Unlock()
		parameterSharedUnitGrant(t, a, "shared_unit.after_import", false)
		if next := parameterSharedUnitCoherent(t, "shared_unit.after_import", a, b); next.Address().ObjectID <= imported.Address().ObjectID { t.Fatal("imported shared OID collided") }
		a.Mu.Lock()
		b.Catalog.InstallOIDFrontierLocked(^ids.OID(0))
		_, err = (core.DDLHost{E: a}).AllocateSharedOID()
		a.Mu.Unlock()
		if err == nil || a.Catalog.NextOID() == 0 || b.Catalog.NextOID() == 0 { t.Fatal("allocation exhaustion wrapped", err) }
	})
}

func TestParameterACLSharedRetirement(t *testing.T) {
	t.Run("queued-retirement", parameterSharedRetirementContention)
	a, b := parameterSharedUnitFixture(t)
	parameterSharedUnitGrant(t, a, "shared_unit.retire", false)
	old, roles := parameterSharedUnitDescriptor(a, "shared_unit.retire"), a.RoleStore.Published
	backing := &old.ACL.Entries[0]
	reader := a.Roles().LookupRef("grant_reader")
	s := enginetest.StartSession(t, b, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "DROP OWNED BY grant_reader CASCADE")
	journal := s.Transactions.Catalog
	if journal == nil || journal.Descriptors[old.Address()] != nil || a.Catalog.State.Get(old.Address()) != old { t.Fatal("cleanup was not private") }
	enginetest.SessionSQL(t, s, "DROP ROLE grant_reader")
	enginetest.SessionSQL(t, s, "COMMIT")
	if parameterSharedUnitCoherent(t, "shared_unit.retire", a, b) != nil || a.Roles().LookupRef("grant_reader").ID() != 0 { t.Fatal("shared cleanup/retirement failed") }
	if roles.Root.Read(reader).LookupRef("grant_reader") != reader || &old.ACL.Entries[0] != backing { t.Fatal("retirement mutated historical authority") }
	enginetest.SessionSQL(t, s, "CREATE ROLE grant_reader LOGIN")
	if a.Roles().LookupRef("grant_reader") == reader { t.Fatal("retirement identity reused") }
	// A true remote-local dependency remains a blocker even when shared copies
	// have been removed from the candidate.
	sa := enginetest.StartSession(t, a, nil)
	enginetest.SessionSQL(t, sa, "CREATE TABLE shared_owned(id INT)")
	enginetest.SessionSQL(t, sa, "GRANT SELECT ON shared_owned TO grant_reader")
	ref := a.Roles().LookupRef("grant_reader")
	a.Mu.Lock()
	dependencies := (core.DDLHost{E: b}).OtherDatabaseDependencies(ref)
	a.Mu.Unlock()
	if len(dependencies) != 1 { t.Fatal("real remote-local dependency was suppressed", dependencies) }
}

func parameterSharedRetirementContention(t *testing.T) {
	a, b := parameterSharedUnitFixture(t)
	principal := a.Roles().LookupRef("grant_reader")
	scope, _, done := parameterSharedUnitPending(t, a, "shared_unit.retirement_race")
	defer done()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiter := enginetest.StartSession(t, b, nil)
	op, err := waiter.Acquire(ctx)
	if err != nil { scope.Finish(err); t.Fatal(err) }
	statement := enginetest.Statement(t, "DROP ROLE grant_reader")
	finished := make(chan error, 1)
	go func() { _, err := b.Execute(op, statement, 0, nil); op.Done(); finished <- err }()
	joined, settled := false, false
	defer func() {
		cancel()
		if !settled { scope.Finish(context.Canceled) }
		if !joined { <-finished }
	}()
	enginetest.LockPending(t, a, 1)
	resource := lock.Resource{ObjectRef: a.Cluster.RoleDomain.Ref(1, ids.ObjectAddress{ClassID: ids.RoleClassID, ObjectID: ids.OID(principal.ID())})}
	entry := a.Locks.Inspect().Resources[resource]
	if entry == nil || len(entry.Pending) != 1 || entry.Pending[0].Mode != lock.AccessExclusive || entry.Holders[scope.LockTicket].Mode != lock.AccessShare { t.Fatal("retirement did not queue behind actual grant dependency", entry) }
	err = scope.Finish(nil)
	settled = true
	if err != nil { t.Fatal(err) }
	err = <-finished
	joined = true
	if err == nil || a.Roles().LookupRef("grant_reader") != principal { t.Fatal("retirement left a dangling shared ACL", err) }
	current := parameterSharedUnitCoherent(t, "shared_unit.retirement_race", a, b)
	if current == nil { t.Fatal("winner grant disappeared") }
	for _, entry := range current.ACL.Entries {
		if err := a.Roles().ValidatePrincipal(entry.Grantor); err != nil { t.Fatal(err) }
		if !entry.Grantee.Public {
			if err := a.Roles().ValidatePrincipal(entry.Grantee.Role); err != nil { t.Fatal(err) }
		}
	}
}

func parameterSharedRetainedAddress(t *testing.T) {
	a, b := parameterSharedUnitFixture(t)
	parameterSharedUnitGrant(t, a, "shared_unit.address", false)
	parameter := parameterSharedUnitCoherent(t, "shared_unit.address", a, b)
	op, done := enginetest.RoleOperation(t, b, "postgres")
	defer done()
	scope, err := b.StartStatement(op)
	if err != nil { t.Fatal(err) }
	defer func() { if !scope.Completed { scope.Finish(context.Canceled) } }()
	// StartStatement owns admission, but Gate creates the genuine transaction
	// ticket/epoch before any executor may claim catalog resources.
	if err := scope.Gate(parameterSpaceRequest("parameter", "shared_unit.address")); err != nil { t.Fatal(err) }
	local := lock.Resource{Kind: lock.CatalogACLResource, ObjectRef: b.Catalog.Domain.Ref(b.Catalog.State.Epoch(), parameter.Address())}
	if err := scope.AcquireCatalogResource(local, lock.AccessExclusive); err != nil { t.Fatal(err) }
	shared := local
	shared.ObjectRef = a.Cluster.RoleDomain.Ref(1, parameter.Address())
	state := a.Locks.Inspect(scope.LockTicket)
	entry := state.Resources[shared]
	if state.Resources[local] != nil || entry == nil || entry.Holders[scope.LockTicket].Mode != lock.AccessExclusive { t.Fatal("retained parameter address was not normalized to shared authority") }
	if err := scope.Finish(nil); err != nil { t.Fatal(err) }
}
