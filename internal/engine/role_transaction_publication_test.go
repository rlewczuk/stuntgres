package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
)

// These units construct immutable candidates and exercise the owning package
// publication/restoration functions, not the parser or public SQL adapter.
func roleTransactionalCandidate(t *testing.T, e *Engine, base *access.RoleRecord, name string) (*access.RoleRecord, authorization.RoleDescriptor) {
	t.Helper()
	ref, err := e.RoleStore.PrincipalCandidateLocked()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := e.RoleStore.RevisionCandidateLocked()
	if err != nil {
		t.Fatal(err)
	}
	d := authorization.RoleDescriptor{Ref: ref, Name: name, Inherit: true, ConnectionLimit: -1, AuthIDVersion: revision}
	root, err := base.Root.PutDescriptor(d)
	if err != nil {
		t.Fatal(err)
	}
	return &access.RoleRecord{Root: root}, d
}

func TestRoleTransactionalPublication(t *testing.T) {
	for _, mode := range []string{"standalone", "explicit", "message", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			if mode == "explicit" {
				transactionBegin(t, s)
			}
			op := transactionOperation(t, s)
			owner := s.Transactions
			if mode == "message" {
				owner.State, owner.RetainedMessage = session.TxImplicit, true
			}
			if mode == "cycle" {
				owner.State = session.TxImplicit
				owner.ProtocolCycle = &session.ProtocolCycle{Owner: owner, Active: true}
			}
			var d authorization.RoleDescriptor
			func() {
				e.Mu.Lock()
				defer e.Mu.Unlock()
				original := e.RoleStore.Published
				first, created := roleTransactionalCandidate(t, e, original, "unit_private")
				d = created
				if err := InstallOperationRolesLocked(e, op, first); err != nil {
					t.Fatal(err)
				}
				e.RoleStore.InstallPrincipalFrontierLocked(d.Ref.ID() + 1)
				mark := owner.Catalog
				d.Login, d.AuthIDVersion = true, e.RoleStore.NextRevision()
				root, err := first.Root.PutDescriptor(d)
				if err != nil {
					t.Fatal(err)
				}
				second := &access.RoleRecord{Root: root}
				if err := InstallOperationRolesLocked(e, op, second); err != nil {
					t.Fatal(err)
				}
				if old, _ := first.Root.Read(authorization.PrincipalRef{}).Find(d.Ref); old.Login || old.AuthIDVersion == d.AuthIDVersion {
					t.Fatal("retained root mutated")
				}
				if mode == "standalone" {
					if e.RoleStore.Published != second || owner.Catalog != nil {
						t.Fatal("standalone mutation retained an owner journal")
					}
				} else {
					if e.RoleStore.Published != original || owner.Catalog == mark || mark.Roles != first || mark.RolesBase != original || owner.Catalog.RolesBase != original {
						t.Fatal("private candidate escaped or first base was replaced")
					}
				}
				if mode == "explicit" {
					changes := owner.Catalog.Clone()
					ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 22001}, ObjectRevision: 2, ObjectName: "unit_private_schema", Owner: d.Ref}
					e.CatalogChangeLocked(changes, ns.Address(), ns)
					owner.Catalog = changes
				}
			}()
			op.Done()
			if mode == "explicit" {
				if !transactionEnd(t, s, owner.Ref, true).Committed {
					t.Fatal("combined publication rolled back")
				}
				func() {
					e.Mu.Lock()
					defer e.Mu.Unlock()
					published, exists := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).Find(d.Ref)
					ns, present := e.Catalog.Current().LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "unit_private_schema"})
					if !exists || !published.Login || !present || e.Catalog.State.Get(ns).(*catalog.Namespace).Owner != d.Ref {
						t.Fatal("roles and catalog did not publish together")
					}
				}()
			} else if mode != "standalone" {
				owner.State, owner.RetainedMessage, owner.ProtocolCycle, owner.Catalog = session.TxIdle, false, nil, nil
			}
		})
	}
	for _, producerRead := range []bool{false, true} {
		name := "completed_metadata_then_first_role_delta"
		if producerRead {
			name = "producer_read_fence_survives_first_role_delta"
		}
		t.Run(name, func(t *testing.T) {
			e := New()
			s, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
			ref := transactionBegin(t, s)
			op, peerOp := transactionOperation(t, s), transactionOperation(t, peer)
			var stage *CatalogStage
			defer func() { e.DiscardCatalogStage(stage) }()
			var ownFirst, ownSecond, peerFirst, peerSecond authorization.RoleDescriptor
			var peerRoot *access.RoleRecord
			var catalogRoot *catalog.State
			var frontier ids.CommitSeq
			func() {
				e.Mu.Lock()
				defer e.Mu.Unlock()
				base := e.RoleStore.Published
				auth, err := e.AuthorizationLocked(op)
				if err != nil {
					t.Fatal(err)
				}
				stage = e.BeginCatalogStage(auth.Catalog)
				stage.AuthorizeLocked(auth, s)
				authorityOnly := session.NewJournal()
				if producerRead {
					// Carry an existing producer obligation through later metadata.
					// The actual producer-routing matrix is covered independently
					// by TestDriverBatchProducerRoleAuthorityLifetime/Savepoint.
					authorityOnly.AddFence(NewCatalogRoleReadFence(CatalogRoleReadFenceFields{Roles: base}))
				}
				if err := EngineCaptureCatalogStageLocked(e, stage, authorityOnly, e.Catalog.State, auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)); err != nil {
					t.Fatal(err)
				}
				if authorityOnly.RolesBase != nil || authorityOnly.Roles != nil {
					t.Fatal("completed catalog metadata fabricated role-write authority")
				}
				s.Transactions.Catalog = authorityOnly
				put := func(operation *session.Operation, predecessor *access.RoleRecord, name string) authorization.RoleDescriptor {
					next, d := roleTransactionalCandidate(t, e, predecessor, name)
					if err := InstallOperationRolesLocked(e, operation, next); err != nil {
						t.Fatal(err)
					}
					e.RoleStore.InstallPrincipalFrontierLocked(d.Ref.ID() + 1)
					return d
				}
				peerFirst = put(peerOp, e.RoleStore.Published, "base_peer_first")
				actualBase := e.RoleStore.Published
				ownFirst = put(op, e.OperationRolesLocked(op), "base_own_first")
				first := s.Transactions.Catalog
				if first.RolesBase != actualBase || first.Roles.Root.Read(authorization.PrincipalRef{}).LookupRef(peerFirst.Name) != peerFirst.Ref || authorityOnly.Roles != nil || authorityOnly.RolesBase != nil {
					t.Fatal("first actual delta used stale authority or mutated its saved journal")
				}
				ownSecond = put(op, e.OperationRolesLocked(op), "base_own_second")
				if s.Transactions.Catalog.RolesBase != actualBase || first.Roles.Root.Read(authorization.PrincipalRef{}).LookupRef(ownSecond.Name) != (authorization.PrincipalRef{}) {
					t.Fatal("subsequent delta replaced the first actual base or changed its saved root")
				}
				peerSecond = put(peerOp, e.RoleStore.Published, "base_peer_second")
				peerRoot, catalogRoot, frontier = e.RoleStore.Published, e.Catalog.State, e.Visibility.CommitSeq()
			}()
			op.Done()
			peerOp.Done()
			commit := transactionOperation(t, s)
			outcome, err := e.Commit(commit, ref)
			commit.Done()
			if producerRead {
				var conflict catalog.Conflict
				if !errors.As(err, &conflict) || outcome.Committed || e.RoleStore.Published != peerRoot || e.Catalog.State != catalogRoot || e.Visibility.CommitSeq() != frontier {
					t.Fatal("pending producer authority did not veto publication atomically", outcome, err)
				}
			} else if err != nil || !outcome.Committed {
				t.Fatal("disjoint first and later peer deltas did not commit", outcome, err)
			}
			for _, expected := range []authorization.RoleDescriptor{peerFirst, peerSecond} {
				if got, exists := e.Roles().Find(expected.Ref); !exists || got != expected {
					t.Fatal("lost committed peer role", expected, got)
				}
			}
			for _, expected := range []authorization.RoleDescriptor{ownFirst, ownSecond} {
				got, exists := e.Roles().Find(expected.Ref)
				if exists == producerRead || exists && got != expected {
					t.Fatal("wrong private role disposition", expected, got, exists)
				}
			}
		})
	}
	t.Run("canceled_combined_publication", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		ref := transactionBegin(t, s)
		op := transactionOperation(t, s)
		e.Mu.Lock()
		base, catalogBase, frontier := e.RoleStore.Published, e.Catalog.State, e.Visibility.CommitSeq()
		var allocator uint64
		func() {
			defer e.Mu.Unlock()
			next, d := roleTransactionalCandidate(t, e, base, "unit_canceled")
			if err := InstallOperationRolesLocked(e, op, next); err != nil {
				t.Fatal(err)
			}
			e.RoleStore.InstallPrincipalFrontierLocked(d.Ref.ID() + 1)
			allocator = e.RoleStore.NextPrincipal()
		}()
		ctx, cancel := context.WithCancel(op.Context())
		cancel()
		op.Ctx = ctx
		_, err := e.Commit(op, ref)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		e.Mu.Lock()
		unchanged := e.RoleStore.Published == base && e.Catalog.State == catalogBase && e.Visibility.CommitSeq() == frontier && e.RoleStore.NextPrincipal() == allocator
		e.Mu.Unlock()
		if !unchanged {
			t.Fatal("canceled publication changed roots/frontier/allocation")
		}
		op.Done()
		if s.Transactions.State != session.TxIdle || s.Transactions.Catalog != nil {
			t.Fatal("failed Commit did not dispose the canceled transaction")
		}
		if e.RoleStore.NextPrincipal() != allocator {
			t.Fatal("rollback rewound principal identities")
		}
	})
}

func TestRoleTransactionalRetirement(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	op := transactionOperation(t, s)
	var a, b authorization.RoleDescriptor
	var published *access.RoleRecord
	func() {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		first, createdA := roleTransactionalCandidate(t, e, e.RoleStore.Published, "unit_retire_a")
		a = createdA
		if err := InstallOperationRolesLocked(e, op, first); err != nil {
			t.Fatal(err)
		}
		e.RoleStore.InstallPrincipalFrontierLocked(a.Ref.ID() + 1)
		second, createdB := roleTransactionalCandidate(t, e, first, "unit_retire_b")
		b = createdB
		if err := InstallOperationRolesLocked(e, op, second); err != nil {
			t.Fatal(err)
		}
		e.RoleStore.InstallPrincipalFrontierLocked(b.Ref.ID() + 1)
		published = e.RoleStore.Published
	}()
	op.Done()
	ref := transactionBegin(t, s)
	op = transactionOperation(t, s)
	drop := func(d authorization.RoleDescriptor) {
		e.Mu.Lock()
		current := e.OperationRolesLocked(op)
		next := current.Root.Retire(map[authorization.PrincipalRef]authorization.RoleDescriptor{d.Ref: d})
		_, err := publishDroppedRolesLocked(e, op, role.DropRolesPlan{Roles: &access.RoleRecord{Root: next}, Retired: []authorization.PrincipalRef{d.Ref}})
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	drop(a)
	mark := CaptureSavepoint(s.Transactions, "retirement")
	drop(b)
	if len(mark.Catalog.RetiredRoles) != 1 || len(s.Transactions.Catalog.RetiredRoles) != 2 {
		t.Fatal("retirement set shared across candidates")
	}
	RestoreSavepoint(s.Transactions, mark, mark.Settings.Clone())
	e.Locks.Restore(s.Transactions.Ticket, mark.Locks)
	if len(s.Transactions.Catalog.RetiredRoles) != 1 || s.Transactions.Catalog.Roles.Root.Read(authorization.PrincipalRef{}).LookupRef(b.Name) != b.Ref {
		t.Fatal("savepoint lost retirement obligation or surviving exact principal")
	}
	fork := mark.Catalog.Clone()
	delete(fork.RetiredRoles, a.Ref)
	if len(mark.Catalog.RetiredRoles) != 1 {
		t.Fatal("clone changed retained retirement mark")
	}
	target := databaseCreateUnitTarget(t, e, New(), "retirement_unit_database")
	if _, err := databaseCreateUnitPublish(e, context.Background(), "retirement_unit_database", target); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	dependency := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 44000}, ObjectRevision: 2, ObjectName: "late_role_dependency", Owner: a.Ref}
	peerRoot := catalog.CloneState(target.Catalog.State)
	peerRoot.SetDescriptor(dependency.Address(), dependency)
	peerRoot.BindName(catalog.DescriptorNameKey(dependency), dependency.Address())
	target.Catalog.PublishLocked(peerRoot)
	localRoot, frontier := e.Catalog.State, e.Visibility.CommitSeq()
	err := EngineRoleRetirementPreflightLocked(e, mark.Catalog, e.PrivateCatalogLocked(mark.Catalog).State())
	unchanged := e.Catalog.State == localRoot && target.Catalog.State == peerRoot && e.RoleStore.Published == published && e.Visibility.CommitSeq() == frontier
	e.Mu.Unlock()
	if !errors.Is(err, ids.ErrCatalogReplaced) || !unchanged {
		t.Fatal("late other-database retirement preflight changed a committed root", err)
	}
	op.Done()
	transactionEnd(t, s, ref, false)
	if e.RoleStore.Published != published || e.Roles().LookupRef(a.Name) != a.Ref || e.Roles().LookupRef(b.Name) != b.Ref {
		t.Fatal("outer rollback changed committed principal identities")
	}
}
