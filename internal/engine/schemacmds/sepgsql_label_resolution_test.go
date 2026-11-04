package schemacmds_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/sqlstate"
)

func sepgsqlResolutionEngine(t *testing.T) *core.Engine {
	t.Helper()
	e := engine.New()
	policy, err := sepgsql.Compile(sepgsql.SimulationConfig{Policy: sepgsql.Policy{
		ID: "label-resolution-owner", Version: "1", Contexts: []string{"A", "P", "U"}, InitialContext: "A", UnlabeledContext: "U",
		Rules:       []sepgsql.Rule{{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}}},
		DefaultSpec: "db_database * P\ndb_schema * P\ndb_table * P\ndb_column * P\ndb_procedure * P\n",
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.InstallSepgsqlSimulation(policy, false)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"CREATE ROLE resolution_actor SUPERUSER LOGIN",
		"CREATE TABLE resolution_table(v integer)",
		"CREATE PUBLICATION resolution_pub FOR TABLE resolution_table",
		"CREATE FUNCTION resolution_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RETURN; END$$",
		"CREATE EVENT TRIGGER resolution_evt ON ddl_command_end WHEN TAG IN ('ALTER TABLE') EXECUTE FUNCTION resolution_fn()",
		"ALTER EVENT TRIGGER resolution_evt DISABLE",
		"CREATE SUBSCRIPTION resolution_sub CONNECTION 'application_name=resolution_unit' PUBLICATION resolution_pub WITH (connect=false,slot_name=NONE)",
		"SELECT lo_from_bytea(921851::oid,decode('00abff','hex'))",
		"CREATE TYPE resolution_type AS ENUM ('v')",
		"CREATE DOMAIN resolution_domain AS integer",
		"CREATE SEQUENCE resolution_sequence START 17 INCREMENT 3 CACHE 4",
	} {
		result := enginetest.Run(t, e, q)
		if result.Rows != nil {
			if err := result.Rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	return e
}

func sepgsqlResolutionTargets(e *core.Engine) []struct {
	sql     string
	address ids.ObjectAddress
} {
	view := e.CatalogRead()
	return []struct {
		sql     string
		address ids.ObjectAddress
	}{
		{"PUBLICATION resolution_pub", view.NameAddress(ids.NameKey{ClassID: ids.PublicationClassID, Name: "resolution_pub"})},
		{"EVENT TRIGGER resolution_evt", view.NameAddress(ids.NameKey{ClassID: ids.EventTriggerClassID, Name: "resolution_evt"})},
		{"SUBSCRIPTION resolution_sub", view.NameAddress(catalog.SubscriptionNameKey(view.Owner().DatabaseAddress(), "resolution_sub"))},
		{"LARGE OBJECT 921851", ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 921851}},
		{"TYPE resolution_type", view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"}), Name: "resolution_type"})},
		{"DOMAIN resolution_domain", view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"}), Name: "resolution_domain"})},
		{"TABLESPACE pg_default", ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: catalog.DefaultTablespaceOID}},
		{"TABLESPACE pg_global", ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: catalog.GlobalTablespaceOID}},
	}
}

func sepgsqlResolutionMode(address ids.ObjectAddress) lock.Mode {
	if address.ClassID == ids.TypeClassID {
		return lock.TypeExclusive
	}
	if address.ClassID == ids.RelationClassID {
		return lock.ShareUpdateExclusive
	}
	return lock.AccessExclusive
}

func sepgsqlResolutionDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("label resolution: got %#v (%v), want %#v", got, err, want)
	}
}

func TestSepgsqlSimulationGenericLabelNoPublication(t *testing.T) {
	e := sepgsqlResolutionEngine(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, target := range sepgsqlResolutionTargets(e) {
		if target.address.ObjectID == 0 || e.CatalogRead().Get(target.address) == nil {
			t.Fatal("proof requires an actual descriptor", target)
		}
		for _, text := range []string{"NULL", "''", "'invalid'", "'P'"} {
			t.Run(target.sql+" IS "+text, func(t *testing.T) {
				stmt := enginetest.Statement(t, "SECURITY LABEL FOR selinux ON "+target.sql+" IS "+text)
				before := e.CatalogRead()
				descriptor := before.Get(target.address)
				labels, roles := before.State().SecurityLabelRoot(), e.RoleStore.Published
				oid, revision, principal := e.Catalog.NextOID(), e.Catalog.NextRevision(), e.RoleStore.NextPrincipal()
				identity, authorizationRevision := op.RoleState(), op.Session.Authorization.Revision
				if _, err := e.Describe(op, stmt); err != nil {
					t.Fatal(err)
				}
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				// Finish is idempotent. On any failed assertion it must release
				// admission before the enclosing RoleOperation ends its session.
				defer scope.Finish(errors.New("label resolution assertion ended before Finish"))
				result, executeErr := scope.Execute(stmt, 0, nil)
				sepgsqlResolutionDiagnostic(t, executeErr, "0A000", "SELinux: security labels are not supported for this type of object")
				// Inspect the genuine in-flight ticket before Finish releases it.
				// Subscription and tablespace claims use shared canonicalization.
				ref := e.Catalog.Domain.Ref(before.Epoch(), target.address)
				if target.address.ClassID == ids.SubscriptionClassID || target.address.ClassID == ids.TablespaceClassID {
					ref = e.Cluster.RoleDomain.Ref(1, target.address)
				}
				if !e.Locks.Holds(scope.LockTicket, lock.Resource{ObjectRef: ref}, sepgsqlResolutionMode(target.address)) {
					t.Error("provider rejection preceded the actual object lock", target)
				}
				sepgsqlResolutionCapture(t, e, target.address, scope.LockTicket, nil, sepgsqlResolutionMode(target.address))
				if target.address.ClassID == ids.TablespaceClassID {
					acl := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(before.Epoch(), target.address), Kind: lock.CatalogACLResource}
					if err := scope.AcquireCatalogResource(acl, lock.AccessExclusive); err != nil || !e.Locks.Holds(scope.LockTicket, acl, lock.AccessExclusive) {
						t.Fatal("ordinary tablespace canonicalization changed the distinct local ACL writer", err)
					}
					sharedACL := acl
					sharedACL.ObjectRef = e.Cluster.RoleDomain.Ref(1, target.address)
					if e.Locks.Holds(scope.LockTicket, sharedACL, lock.AccessExclusive) {
						t.Fatal("tablespace ACL writer was remapped into shared object authority")
					}
				}
				if scope.Completed || scope.Catalog != nil || result.Rows != nil || result.CommandTag != "" || result.Count != 0 || op.Session.Transactions.Catalog != nil {
					t.Error("unsupported class staged a result or private journal", result)
				}
				sepgsqlResolutionDiagnostic(t, scope.Finish(executeErr), "0A000", "SELinux: security labels are not supported for this type of object")
				if e.CatalogRead().State() != before.State() || before.State().SecurityLabelRoot() != labels || before.Get(target.address) != descriptor || e.CatalogRead().Get(target.address) != descriptor ||
					e.RoleStore.Published != roles || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.RoleStore.NextPrincipal() != principal ||
					op.RoleState() != identity || op.Session.Authorization.Revision != authorizationRevision || op.Session.Transactions.Catalog != nil ||
					before.State().SecurityLabel(catalog.SecurityLabelKey{Object: target.address, Provider: "selinux"}) != nil {
					t.Fatal("unsupported class changed retained/current catalog, roles, allocation or session authority")
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

// Revocation while the real object lock is pending must be seen by the fresh
// postlock owner check, even when the selected descriptor itself is unchanged.
func TestSepgsqlSimulationGenericLabelFreshOwnership(t *testing.T) {
	for _, index := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			e := sepgsqlResolutionEngine(t)
			target := sepgsqlResolutionLockTargets(e)[index]
			admin, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			stmt := enginetest.Statement(t, "SECURITY LABEL FOR selinux ON "+target.sql+" IS 'invalid'")
			blocker, err := e.StartStatement(admin)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Finish(nil)
			// StartStatement owns admission, but Gate creates the real
			// transaction incarnation and ticket used by catalog locks.
			if err := blocker.Gate(stmt); err != nil {
				t.Fatal(err)
			}
			before := e.CatalogRead()
			mode := sepgsqlResolutionMode(target.address)
			if err := blocker.AcquireCatalogResource(lock.Resource{ObjectRef: e.Catalog.Domain.Ref(before.Epoch(), target.address)}, mode); err != nil {
				t.Fatal(err)
			}
			ref := e.Catalog.Domain.Ref(before.Epoch(), target.address)
			if target.address.ClassID == ids.SubscriptionClassID || target.address.ClassID == ids.TablespaceClassID {
				ref = e.Cluster.RoleDomain.Ref(1, target.address)
			}
			resource := lock.Resource{ObjectRef: ref}
			if !e.Locks.Holds(blocker.LockTicket, resource, mode) {
				t.Fatal("blocker did not hold the canonical object resource", resource)
			}
			worker := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, worker, "SET ROLE resolution_actor")
			identity, authorizationRevision := worker.Authorization.State, worker.Authorization.Revision
			// The existing owner helper cancels and joins at cleanup even when
			// an assertion fails before the normal join below.
			join := enginetest.ExecuteAsync(t, e, worker, t.Context(), "SECURITY LABEL FOR selinux ON "+target.sql+" IS 'invalid'")
			enginetest.LockPending(t, e, 1)
			entry := e.Locks.Inspect().Resources[resource]
			pending := false
			if entry != nil {
				for _, request := range entry.Pending {
					if !request.Terminal && request.Ticket.Ref().Session() == worker.SessionToken && request.Mode == mode {
						pending = true
					}
				}
			}
			if !pending {
				t.Fatal("label owner did not wait on its canonical object resource", resource)
			}
			enginetest.Run(t, e, "ALTER ROLE resolution_actor NOSUPERUSER")
			roles := e.RoleStore.Published
			state := e.CatalogRead().State()
			oid, revision := e.Catalog.NextOID(), e.Catalog.NextRevision()
			principal := e.RoleStore.NextPrincipal()
			labels := before.State().SecurityLabelRoot()
			label := before.State().SecurityLabel(catalog.SecurityLabelKey{Object: target.address, Provider: "selinux"})
			if err := blocker.Finish(nil); err != nil {
				t.Fatal(err)
			}
			result, executeErr := join()
			message := []string{"must be owner of publication resolution_pub", "must be owner of event trigger resolution_evt", "must be owner of subscription resolution_sub", "must be owner of large object 921851", "must be owner of type resolution_type", "must be owner of type resolution_domain", "must be owner of tablespace pg_default", "must be owner of tablespace pg_global", "must be owner of sequence resolution_sequence"}[index]
			sepgsqlResolutionDiagnostic(t, executeErr, "42501", message)
			if result.Rows != nil || result.CommandTag != "" || result.Count != 0 {
				t.Fatal("revoked owner produced a result", result)
			}
			if e.CatalogRead().State() != state || e.RoleStore.Published != roles || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.RoleStore.NextPrincipal() != principal ||
				e.CatalogRead().Get(target.address) != before.Get(target.address) || before.State().SecurityLabelRoot() != labels || e.CatalogRead().State().SecurityLabelRoot() != labels ||
				before.State().SecurityLabel(catalog.SecurityLabelKey{Object: target.address, Provider: "selinux"}) != label ||
				e.CatalogRead().State().SecurityLabel(catalog.SecurityLabelKey{Object: target.address, Provider: "selinux"}) != label ||
				worker.Authorization.State != identity || worker.Authorization.Revision != authorizationRevision || worker.Transactions.Catalog != nil {
				t.Fatal("revoked owner changed the selected descriptor, catalog, allocations or journal")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

// Capture the records owned by the actual ExecuteSecurityLabel waiter and
// blocker, then pass those same values through the public pg_locks projection.
func sepgsqlResolutionCapture(t *testing.T, e *core.Engine, address ids.ObjectAddress, holder, waiter *lock.Ticket, mode lock.Mode) {
	t.Helper()
	if address.ClassID == ids.RelationClassID {
		sepgsqlIntegrityRelationCapture(t, e, address, holder, waiter, mode)
		return
	}
	records, err := e.Locks.PGLocks(e.Catalog.Domain, e.CatalogRead().Epoch())
	if err != nil {
		t.Fatal(err)
	}
	class, shared := uint32(address.ClassID), address.ClassID == ids.TablespaceClassID || address.ClassID == ids.SubscriptionClassID
	if address.ClassID == ids.LargeObjectClassID {
		class = 2613
		if address.ClassID != 2995 {
			t.Fatal("large object lost canonical metadata authority", address)
		}
	}
	grants, waits := 0, 0
	for _, record := range records {
		if record.Kind != lock.ObjectRecord || record.Class != class || record.Object != uint32(address.ObjectID) {
			continue
		}
		wantOwner := lock.RecordOwner{Session: holder.Ref().Session().ID(), Local: holder.Ref().ID()}
		if record.Waiting {
			if waiter == nil {
				t.Fatal("uncontended label unexpectedly has a pending object request", record)
			}
			waits++
			wantOwner = lock.RecordOwner{Session: waiter.Ref().Session().ID(), Local: waiter.Ref().ID()}
		} else {
			grants++
		}
		if record.Owner != wantOwner || record.Mode != mode || record.Shared != shared || record.Subobject != 0 || record.XID != 0 || record.Target != (lock.RecordOwner{}) || record.Waiting == record.WaitStart.IsZero() {
			t.Fatal("composed object capture changed identity, mode, ownership or wait", record)
		}
		source := e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.LocksOID})
		rows, err := pgcatalog.LockRows(t.Context(), source, uint32(e.CatalogRead().Owner().DatabaseAddress().ObjectID), []lock.Record{record}, nil)
		if err != nil || len(rows) != 1 {
			t.Fatal("actual object record failed public projection", rows, err)
		}
		row := rows[0]
		kind, _ := row[0].Text()
		database, _ := row[1].OID()
		publicClass, _ := row[7].OID()
		object, _ := row[8].OID()
		subobject, _ := row[9].Integer()
		virtual, _ := row[10].Text()
		pid, _ := row[11].Integer()
		publicMode, _ := row[12].Text()
		granted, _ := row[13].Boolean()
		fastpath, _ := row[14].Boolean()
		wantDatabase := uint32(e.CatalogRead().Owner().DatabaseAddress().ObjectID)
		if shared {
			wantDatabase = 0
		}
		wantMode := "AccessExclusiveLock"
		if mode == lock.TypeExclusive {
			wantMode = "ExclusiveLock"
		}
		if kind != "object" || database != wantDatabase || publicClass != class || object != uint32(address.ObjectID) || subobject != 0 ||
			virtual != fmt.Sprintf("%d/%d", wantOwner.Session, wantOwner.Local) || pid != int64(wantOwner.Session) || publicMode != wantMode || granted == record.Waiting || fastpath ||
			!row[2].IsNull() || !row[3].IsNull() || !row[4].IsNull() || !row[5].IsNull() || !row[6].IsNull() || row[15].IsNull() == record.Waiting {
			t.Fatal("composed public object lock identity/null/owner/grant matrix", row)
		}
		if record.Waiting {
			stamp, err := row[15].TimestampTime()
			if err != nil || !stamp.Equal(record.WaitStart) {
				t.Fatal("public wait timestamp changed", stamp, record.WaitStart, err)
			}
		}
	}
	wantWaits := 0
	if waiter != nil {
		wantWaits = 1
	}
	if grants != 1 || waits != wantWaits {
		t.Fatal("composed capture lost actual grant/pending ownership", grants, waits, wantWaits)
	}
}

func TestSepgsqlSimulationGenericLabelLockLifecycle(t *testing.T) {
	for _, index := range []int{3, 4, 5, 6, 8} {
		for _, history := range []string{"release", "cancel", "descriptor", "epoch"} {
			t.Run(fmt.Sprintf("%d/%s", index, history), func(t *testing.T) {
				e := sepgsqlResolutionEngine(t)
				target := sepgsqlResolutionLockTargets(e)[index]
				workerEngine := e
				if target.address.ClassID == ids.TablespaceClassID {
					// Real shared metadata, including a supported rename, must
					// arbitrate on one RoleDomain resource across database roots.
					enginetest.Run(t, e, "CREATE DATABASE resolution_lock_peer")
					enginetest.Run(t, e, "ALTER TABLESPACE pg_default RENAME TO resolution_space")
					enginetest.Run(t, e, "ALTER TABLESPACE resolution_space OWNER TO resolution_actor")
					enginetest.Run(t, e, "ALTER ROLE resolution_actor NOSUPERUSER")
					target.sql = "TABLESPACE resolution_space"
					var err error
					workerEngine, err = e.SelectDatabase(t.Context(), "resolution_lock_peer")
					if err != nil {
						t.Fatal(err)
					}
					if workerEngine.Catalog.Domain == e.Catalog.Domain || workerEngine.CatalogRead().Get(target.address) != e.CatalogRead().Get(target.address) {
						t.Fatal("tablespace witness needs distinct roots and the same real descriptor")
					}
				}
				admin, finish := enginetest.RoleOperation(t, e, "postgres")
				defer finish()
				stmt := enginetest.Statement(t, "SECURITY LABEL FOR selinux ON "+target.sql+" IS 'invalid'")
				blocker, err := e.StartStatement(admin)
				if err != nil {
					t.Fatal(err)
				}
				defer blocker.Finish(nil)
				if err := blocker.Gate(stmt); err != nil {
					t.Fatal(err)
				}
				before, peerBefore := e.CatalogRead(), workerEngine.CatalogRead()
				expected, peerExpected := before, peerBefore
				labels, peerLabels := before.State().SecurityLabelRoot(), peerBefore.State().SecurityLabelRoot()
				roles, oid, revision, principal := e.RoleStore.Published, e.Catalog.NextOID(), e.Catalog.NextRevision(), e.RoleStore.NextPrincipal()
				mode := sepgsqlResolutionMode(target.address)
				original := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(before.Epoch(), target.address)}
				if err := blocker.AcquireCatalogResource(original, mode); err != nil {
					t.Fatal(err)
				}
				canonical := original
				if target.address.ClassID == ids.TablespaceClassID {
					canonical.ObjectRef = e.Cluster.RoleDomain.Ref(1, target.address)
					if e.Locks.Holds(blocker.LockTicket, original, mode) {
						t.Fatal("tablespace granted the original local rather than shared authority")
					}
				}
				worker := enginetest.StartSession(t, workerEngine, nil)
				if target.address.ClassID == ids.TablespaceClassID {
					enginetest.SessionSQL(t, worker, "SET ROLE resolution_actor")
				}
				identity, authorizationRevision := worker.Authorization.State, worker.Authorization.Revision
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				join := enginetest.ExecuteAsync(t, workerEngine, worker, ctx, "SECURITY LABEL FOR selinux ON "+target.sql+" IS 'invalid'")
				enginetest.LockPending(t, e, 1)
				entry := e.Locks.Inspect().Resources[canonical]
				var waiter *lock.Ticket
				if entry != nil {
					for _, pending := range entry.Pending {
						if !pending.Terminal && pending.Mode == mode && pending.Ticket.Ref().Session() == worker.SessionToken {
							waiter = pending.Ticket
						}
					}
				}
				if waiter == nil || !e.Locks.Holds(blocker.LockTicket, canonical, mode) {
					t.Fatal("real SECURITY LABEL did not queue behind the actual canonical grant", canonical)
				}
				sepgsqlResolutionCapture(t, e, target.address, blocker.LockTicket, waiter, mode)
				if workerEngine != e {
					sepgsqlResolutionCapture(t, workerEngine, target.address, blocker.LockTicket, waiter, mode)
				}
				if history == "descriptor" {
					newOwner := "resolution_actor"
					if target.address.ClassID == ids.TablespaceClassID {
						newOwner = "postgres"
					}
					if target.address.ClassID == ids.RelationClassID {
						// OWNER's ShareRowExclusive conflicts with this blocker.
						// The genuine ACL writer uses compatible AccessShare and
						// a distinct CatalogACLResource, replacing this descriptor.
						old := before.MustRelation(target.address)
						e.Mu.Lock()
						auth, authErr := e.AuthorizationLocked(admin)
						e.Mu.Unlock()
						if authErr != nil {
							t.Fatal(authErr)
						}
						actor, ok := auth.Roles.Lookup("resolution_actor")
						if !ok || authorization.ACLRights(old.Security.ACL, old.Owner, []authorization.PrincipalRef{actor.Ref}, authorization.SelectPrivilege, false) != 0 {
							t.Fatal("sequence descriptor witness needs absent SELECT")
						}
						generator := *e.Storage.Sequences[target.address]
						enginetest.Run(t, e, "GRANT SELECT ON SEQUENCE resolution_sequence TO resolution_actor")
						next := e.CatalogRead().MustRelation(target.address)
						if next.Owner != old.Owner || next.Name() != old.Name() || next.Address() != old.Address() ||
							*next.Sequence != *old.Sequence || *e.Storage.Sequences[target.address] != generator ||
							authorization.ACLRights(next.Security.ACL, next.Owner, []authorization.PrincipalRef{actor.Ref}, authorization.SelectPrivilege, false) != authorization.SelectPrivilege {
							t.Fatal("sequence GRANT did not preserve identity/owner/generator and publish its deliberate ACL")
						}
					} else {
						enginetest.Run(t, e, "ALTER "+target.sql+" OWNER TO "+newOwner)
					}
					expected, peerExpected = e.CatalogRead(), workerEngine.CatalogRead()
					if expected.Get(target.address) == before.Get(target.address) {
						t.Fatal("descriptor fence needs a genuine replacement")
					}
					roles, oid, revision, principal = e.RoleStore.Published, e.Catalog.NextOID(), e.Catalog.NextRevision(), e.RoleStore.NextPrincipal()
				}
				switch history {
				case "cancel":
					cancel()
				case "epoch":
					// Fence the waiting transaction's actual local domain,
					// including a peer waiting on a shared tablespace grant.
					e.Locks.Fence(workerEngine.Catalog.Domain, peerBefore.Epoch()+1)
				default:
					if err := blocker.Finish(nil); err != nil {
						t.Fatal(err)
					}
				}
				result, executeErr := join()
				switch history {
				case "cancel":
					if !errors.Is(executeErr, context.Canceled) || !e.Locks.Holds(blocker.LockTicket, canonical, mode) || e.Locks.Holds(waiter, canonical, mode) {
						t.Fatal("cancel changed blocker or admitted canceled label owner", executeErr)
					}
					if err := blocker.Finish(nil); err != nil {
						t.Fatal(err)
					}
				case "epoch":
					if !errors.Is(executeErr, ids.ErrCatalogReplaced) {
						t.Fatal("old-incarnation label wait survived epoch fencing", executeErr)
					}
					if err := blocker.Finish(nil); err != nil && !errors.Is(err, ids.ErrCatalogReplaced) {
						t.Fatal(err)
					}
				case "descriptor":
					var conflict catalog.Conflict
					if !errors.As(executeErr, &conflict) || executeErr.Error() != "stuntgres: catalog publication conflict" {
						t.Fatal("label adopted a replacement descriptor after waiting", executeErr)
					}
				default:
					if target.address.ClassID == ids.RelationClassID {
						var diagnostic *sqlstate.Error
						want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot set security label on relation "resolution_sequence"`, Detail: "This operation is not supported for sequences."}
						if !errors.As(executeErr, &diagnostic) || *diagnostic != want {
							t.Fatal("sequence integrity after actual wait", diagnostic, executeErr)
						}
					} else {
						sepgsqlResolutionDiagnostic(t, executeErr, "0A000", "SELinux: security labels are not supported for this type of object")
					}
				}
				if result.Rows != nil || result.CommandTag != "" || result.Count != 0 ||
					e.CatalogRead().State() != expected.State() || workerEngine.CatalogRead().State() != peerExpected.State() ||
					e.CatalogRead().Get(target.address) != expected.Get(target.address) || workerEngine.CatalogRead().Get(target.address) != peerExpected.Get(target.address) ||
					before.State().SecurityLabelRoot() != labels || peerBefore.State().SecurityLabelRoot() != peerLabels ||
					e.RoleStore.Published != roles || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.RoleStore.NextPrincipal() != principal ||
					worker.Authorization.State != identity || worker.Authorization.Revision != authorizationRevision || worker.Transactions.Catalog != nil {
					t.Fatal("generic label lock lifecycle published state, allocated identities or leaked a journal", result)
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
