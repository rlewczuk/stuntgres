package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The old pre-canceled-operation witness never enters retirement. These cases
// cross genuine FORCE preflight and termination, then retain the victim's real
// operation lease while changing cancellation or publication authority. Already
// requested termination is intentionally irreversible; logical roots are not.
func TestDatabaseDropCancellation(t *testing.T) {
	for _, boundary := range []string{
		"cancel_during_terminal_drain",
		"cancel_after_terminal_drain_before_publication",
		"owner_inheritance_revoked_during_drain",
		"revision_exhausted_after_preflight",
		"fresh_shared_publication_during_drain",
	} {
		t.Run(boundary, func(t *testing.T) {
			e, target, admin, issuer, victim := databaseDropCancellationFixture(t)
			peerOwner := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peerOwner, "SELECT pg_advisory_lock(934901)")
			retained := enginetest.Run(t, target, "SELECT v + 0 FROM drop_cancel_backing")
			assertOutput := databaseDropCancellationOutput(t, retained)
			ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
			defer cancel()
			dropCtx, cancelDrop := context.WithCancel(ctx)
			defer cancelDrop()
			lease, err := victim.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			joined := false
			replacementHeld := false
			// Release first: terminal cleanup needs this exact operation lease.
			// Cancellation and join are installed before launching the issuer.
			defer func() {
				lease.Done()
				cancelDrop()
				if replacementHeld {
					target.ReplacementMu.Unlock()
				}
				if !joined {
					<-result
				}
			}()
			go func() {
				_, err := databaseDropOwnerExecute(dropCtx, e, issuer, databaseDropOwnerStatement(true))
				result <- err
			}()
			select {
			case <-victim.EndRequested():
			case <-ctx.Done():
				t.Fatal("FORCE did not reach real victim termination", ctx.Err())
			}
			resource := e.DatabaseResource(target.Catalog.DatabaseAddress())
			grant := e.Locks.Inspect().Resources[resource]
			if grant == nil || len(grant.Holders) != 1 || len(grant.Pending) != 0 {
				t.Fatal("draining DROP does not own its actual AX grant", grant)
			}
			for ticket, hold := range grant.Holders {
				if ticket.Persistent() || ticket.Session() != issuer.SessionToken || hold.Mode != lock.AccessExclusive {
					t.Fatal("wrong draining utility authority", ticket, hold)
				}
			}
			if core.EngineDatabaseAdmissionCount(target) != 1 {
				t.Fatal("FORCE released the ending physical owner before lease drain")
			}
			select {
			case err := <-result:
				joined = true
				t.Fatal("FORCE published before real terminal completion", err)
			default:
			}

			// These are real disjoint SQL publications while the target is
			// draining. They cannot be overwritten by a saved pre-drain root.
			enginetest.SessionSQL(t, admin, "ALTER ROLE drop_cancel_owner SET application_name='new-global'")
			if _, err := databaseDropOwnerExecute(ctx, e, admin, enginetest.Statement(t, "CREATE DATABASE drop_during_drain")); err != nil {
				t.Fatal("DROP held metadata across terminal drain", err)
			}
			enginetest.SessionSQL(t, admin, "ALTER DATABASE drop_during_drain RENAME TO drop_during_drain_renamed")
			enginetest.SessionSQL(t, admin, "GRANT CONNECT ON DATABASE drop_during_drain_renamed TO drop_cancel_issuer")
			enginetest.SessionSQL(t, admin, "ALTER ROLE drop_cancel_owner IN DATABASE drop_during_drain_renamed SET lock_timeout='17ms'")
			if boundary == "owner_inheritance_revoked_during_drain" {
				enginetest.SessionSQL(t, admin, "REVOKE drop_cancel_owner FROM drop_cancel_issuer")
			}
			if boundary == "revision_exhausted_after_preflight" {
				// Same actual RoleStore revision authority as the existing early
				// exhaustion proof, but only AFTER FORCE has signaled its victim.
				e.Mu.Lock()
				record := e.RoleStore.Published.Advance()
				record.Revision = ^uint64(0)
				e.RoleStore.PublishLocked(record)
				e.Mu.Unlock()
			}
			survivor := e.Cluster.Targets["drop_during_drain_renamed"]
			descriptor := survivor.CatalogRead().Get(survivor.Catalog.DatabaseAddress())
			owner := e.Roles().LookupRef("drop_cancel_owner")
			globalKey := access.RoleSettingKey{Role: owner}
			qualifiedKey := access.RoleSettingKey{Role: owner, Database: survivor.Catalog.DatabaseAddress()}
			roles := e.RoleStore.Published
			globalRow, qualifiedRow := roles.Settings.Lookup(globalKey), roles.Settings.Lookup(qualifiedKey)
			peerRef := enginetest.RoleTableRef(t, e, "drop_cancel_backing")
			peerHeap := e.Storage.Relations[peerRef.Address]
			peerLabelKey := catalog.SecurityLabelKey{Object: e.Catalog.DatabaseAddress(), Provider: "selinux"}
			peerLabel := e.CatalogRead().State().SecurityLabel(peerLabelKey)
			if len(globalRow) != 1 || len(qualifiedRow) != 1 || peerLabel == nil {
				t.Fatal("missing independently published survivor backing")
			}
			unchanged := databaseDropCancellationUnchanged(t, e, target, peerOwner)
			if boundary == "cancel_during_terminal_drain" {
				cancelDrop()
				err = databaseDropReceive(t, result)
				joined = true
				if !errors.Is(err, context.Canceled) {
					t.Fatal("draining cancellation outcome", err)
				}
				// Cancellation must return without falsely waiting for or undoing
				// the victim's irreversible termination request.
				select {
				case <-victim.Completed:
					t.Fatal("canceled DROP bypassed the retained terminal lease")
				default:
				}
				unchanged()
				assertOutput()
				// This admission must work before the old ending owner completes:
				// only this failed attempt's transient exclusion was removed.
				a, err := e.AdmitDatabase(ctx, admin.CountedAuthenticated, enginetest.NextSessionID(), core.DatabaseSelector{Name: target.DatabaseName, Explicit: true})
				if err != nil {
					t.Fatal("canceled attempt left target draining", err)
				}
				a.Release()
				lease.Done()
				if err := victim.WaitEnd(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				if boundary == "cancel_after_terminal_drain_before_publication" {
					// Hold the genuine final replacement-writer gate, not
					// metadata, while the physical victim finishes. No final
					// installation can precede the subsequent cancellation.
					target.ReplacementMu.Lock()
					replacementHeld = true
				}
				lease.Done()
				if replacementHeld {
					if err := victim.WaitEnd(ctx); err != nil {
						t.Fatal(err)
					}
					cancelDrop()
					target.ReplacementMu.Unlock()
					replacementHeld = false
				}
				err = databaseDropReceive(t, result)
				joined = true
				switch boundary {
				case "cancel_after_terminal_drain_before_publication":
					if !errors.Is(err, context.Canceled) {
						t.Fatal("post-drain cancellation published retirement", err)
					}
				case "owner_inheritance_revoked_during_drain":
					enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "must be owner of database drop_owner_target"})
				case "revision_exhausted_after_preflight":
					if err == nil || err.Error() != "stuntgres: authorization revision exhausted" {
						t.Fatal("late revision veto did not preserve publication", err)
					}
				case "fresh_shared_publication_during_drain":
					if err != nil {
						t.Fatal("disjoint shared changes vetoed retirement", err)
					}
				}
				if boundary != "fresh_shared_publication_during_drain" {
					unchanged()
				}
			}
			select {
			case <-victim.Completed:
			default:
				t.Fatal("released physical victim did not actually complete")
			}
			if core.EngineDatabaseAdmissionCount(target) != 0 {
				t.Fatal("terminal admission survived failed/successful DROP")
			}
			assertOutput()
			if boundary == "revision_exhausted_after_preflight" {
				return // No invented recovery from a genuinely exhausted authority.
			}
			if boundary == "owner_inheritance_revoked_during_drain" {
				enginetest.SessionSQL(t, admin, "GRANT drop_cancel_owner TO drop_cancel_issuer")
			}
			if boundary != "fresh_shared_publication_during_drain" {
				if _, err := databaseDropOwnerExecute(ctx, e, issuer, databaseDropOwnerStatement(false)); err != nil {
					t.Fatal("new attempt could not retire the preserved incarnation", err)
				}
			}
			if len(target.Storage.Relations) != 0 || e.Cluster.Targets[target.DatabaseName] != nil {
				t.Fatal("eventual retirement did not remove actual target backing")
			}
			assertOutput()
			fresh := e.Cluster.Targets["drop_during_drain_renamed"]
			if fresh != survivor || e.Cluster.Targets["drop_during_drain"] != nil {
				t.Fatal("retirement replaced latest CREATE/rename registry")
			}
			if fresh.CatalogRead().Get(fresh.Catalog.DatabaseAddress()) != descriptor || e.Storage.Relations[peerRef.Address] != peerHeap || e.CatalogRead().State().SecurityLabel(peerLabelKey) != peerLabel {
				t.Fatal("retirement replaced latest peer descriptor, heap or label backing")
			}
			retainedGlobal, retainedQualified := e.RoleStore.Published.Settings.Lookup(globalKey), e.RoleStore.Published.Settings.Lookup(qualifiedKey)
			if len(retainedGlobal) != 1 || len(retainedQualified) != 1 || &retainedGlobal[0] != &globalRow[0] || &retainedQualified[0] != &qualifiedRow[0] {
				t.Fatal("retirement recopied unrelated default-row backing")
			}
			if boundary != "owner_inheritance_revoked_during_drain" && !e.RoleStore.Published.SameAuthority(roles) {
				t.Fatal("retirement replaced current principal graph lineage")
			}
			global, qualified := false, false
			for key, row := range e.RoleStore.Published.Settings.Rows() {
				if key.Database == target.Catalog.DatabaseAddress() {
					t.Fatal("retirement retained old target defaults")
				}
				for _, setting := range row {
					global = global || key.Role == owner && key.Database == (ids.ObjectAddress{}) && setting.Name == "application_name" && setting.Value == "new-global"
					qualified = qualified || key.Role == owner && key.Database == fresh.Catalog.DatabaseAddress() && setting.Name == "lock_timeout" && setting.Value == "17ms"
				}
			}
			if !global || !qualified {
				t.Fatal("retirement lost defaults published during terminal drain")
			}
			// The exact descriptor, including its newly committed ACL, survives
			// unchanged; all catalog projections must borrow that same authority.
			for store := range e.Cluster.Catalogs {
				if store.State.Get(fresh.Catalog.DatabaseAddress()) != descriptor {
					t.Fatal("DROP installed stale shared descriptor authority")
				}
			}
		})
	}
}

func databaseDropCancellationFixture(t *testing.T) (*core.Engine, *core.Engine, *session.Session, *session.Session, *session.Session) {
	t.Helper()
	e := engine.NewWithSettings(nil, "drop_cancel_peer", false)
	// This is the real native simulation policy/label path, not a claim of
	// active-provider or PostgreSQL SELinux parity.
	policy, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, Policy: sepgsql.Policy{
		ID: "drop-cancellation", Version: "1", Contexts: []string{"A", "P"}, InitialContext: "A", UnlabeledContext: "P",
		Rules: []sepgsql.Rule{{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.InstallSepgsqlSimulation(policy, true)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	admin := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, admin, "CREATE ROLE drop_cancel_owner LOGIN")
	enginetest.SessionSQL(t, admin, "CREATE ROLE drop_cancel_issuer LOGIN")
	enginetest.SessionSQL(t, admin, "GRANT drop_cancel_owner TO drop_cancel_issuer")
	if _, err := databaseDropOwnerExecute(t.Context(), e, admin, enginetest.Statement(t, "CREATE DATABASE drop_owner_target OWNER drop_cancel_owner")); err != nil {
		t.Fatal(err)
	}
	target, err := e.SelectDatabase(t.Context(), "drop_owner_target")
	if err != nil {
		t.Fatal(err)
	}
	for _, runtime := range []*core.Engine{e, target} {
		enginetest.Run(t, runtime, "CREATE TABLE drop_cancel_backing(v integer)")
		enginetest.Run(t, runtime, "INSERT INTO drop_cancel_backing VALUES(11)")
		enginetest.Run(t, runtime, "CREATE INDEX drop_cancel_index ON drop_cancel_backing(v)")
		enginetest.Run(t, runtime, "CREATE SEQUENCE drop_cancel_sequence START 41")
		enginetest.Run(t, runtime, "ANALYZE drop_cancel_backing")
	}
	enginetest.SessionSQL(t, admin, "ALTER ROLE drop_cancel_owner SET application_name='old-global'")
	enginetest.SessionSQL(t, admin, "ALTER ROLE drop_cancel_owner IN DATABASE drop_owner_target SET lock_timeout='23ms'")
	enginetest.SessionSQL(t, admin, "ALTER DATABASE drop_owner_target SET statement_timeout='31ms'")
	enginetest.SessionSQL(t, admin, "SECURITY LABEL FOR selinux ON DATABASE drop_owner_target IS 'P'")
	enginetest.SessionSQL(t, admin, "SECURITY LABEL FOR selinux ON DATABASE drop_cancel_peer IS 'P'")
	open := func(runtime *core.Engine, name string) *session.Session {
		s, err := enginetest.OpenSession(t.Context(), runtime, session.SessionStart{ID: enginetest.NextSessionID()}, e.Roles().LookupRef(name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			s.RequestEnd(session.CleanClose)
			if err := s.WaitEnd(context.Background()); err != nil {
				t.Error(err)
			}
		})
		return s
	}
	issuer := open(e, "drop_cancel_issuer")
	victim := open(target, "drop_cancel_owner")
	return e, target, admin, issuer, victim
}

// Save actual immutable owners and backing pointers, not freshly reconstructed
// equal SQL output. Capture is after the deliberate disjoint publications; a
// failed attempt may neither erase those nor retire any selected backing.
func databaseDropCancellationUnchanged(t *testing.T, e, target *core.Engine, peerOwner *session.Session) func() {
	t.Helper()
	lockState := e.Locks.Inspect()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	targets := maps.Clone(e.Cluster.Targets)
	roles, principal := e.RoleStore.Published, e.RoleStore.NextPrincipal()
	oid, manager, defaultTarget := e.Cluster.NextOID, e.Locks, e.Cluster.DefaultTarget
	roleDomain, principalDomain, aclAuthority := &e.Cluster.RoleDomain, &e.RoleStore.Domain, &e.RoleStore.ACLAuthority
	var checks []func()
	for _, runtime := range targets {
		root := runtime.Catalog.State
		nextOID, nextRevision := runtime.Catalog.NextOID(), runtime.Catalog.NextRevision()
		heaps, metadata := maps.Clone(runtime.Storage.Relations), maps.Clone(runtime.Storage.RelationMetadata)
		indexes, sequences := maps.Clone(runtime.Storage.IndexRoots), maps.Clone(runtime.Storage.Sequences)
		row, version := runtime.Storage.RowFrontier(), runtime.Storage.VersionFrontier()
		parameter := root.ParameterIncarnation()
		for _, heap := range heaps {
			versions := slices.Clone(heap.Versions)
			for i := range versions {
				versions[i].Values = slices.Clone(versions[i].Values)
			}
			checks = append(checks, func() {
				if !reflect.DeepEqual(heap.Versions, versions) {
					t.Fatal("failed retirement mutated retained physical row backing in place")
				}
			})
		}
		checks = append(checks, func() {
			if runtime.Catalog.State != root || runtime.Catalog.NextOID() != nextOID || runtime.Catalog.NextRevision() != nextRevision || runtime.Catalog.State.ParameterIncarnation() != parameter {
				t.Fatal("failed retirement changed exact catalog/label/dependency roots or frontiers")
			}
			if !maps.Equal(runtime.Storage.Relations, heaps) || !maps.Equal(runtime.Storage.RelationMetadata, metadata) || !maps.Equal(runtime.Storage.IndexRoots, indexes) || !maps.Equal(runtime.Storage.Sequences, sequences) || runtime.Storage.RowFrontier() != row || runtime.Storage.VersionFrontier() != version {
				t.Fatal("failed retirement cleared/copied selected or peer physical backing")
			}
		})
	}
	visibility, assigned, commit := target.Visibility.VisibilityXID(), target.Visibility.LastAssignedXID(), target.Visibility.CommitSeq()
	peerLocks := make(map[lock.Resource]lock.Hold)
	for resource, state := range lockState.Resources {
		if hold, ok := state.Holders[peerOwner.Advisory.Ticket]; ok {
			peerLocks[resource] = hold
		}
	}
	hasDefaults := false
	for range roles.Settings.Rows() {
		hasDefaults = true
	}
	if len(peerLocks) == 0 || target.Catalog.State.SecurityLabel(catalog.SecurityLabelKey{Object: target.Catalog.DatabaseAddress(), Provider: "selinux"}) == nil || !hasDefaults {
		t.Fatal("fixture lacks genuine lock, label or default backing")
	}
	return func() {
		t.Helper()
		state := manager.Inspect()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		if !maps.Equal(e.Cluster.Targets, targets) || e.Cluster.NextOID != oid || e.RoleStore.Published != roles || e.RoleStore.NextPrincipal() != principal || e.Locks != manager || e.Cluster.DefaultTarget != defaultTarget || &e.Cluster.RoleDomain != roleDomain || &e.RoleStore.Domain != principalDomain || &e.RoleStore.ACLAuthority != aclAuthority {
			t.Fatal("failed DROP replaced registry/settings/principal/lock authority")
		}
		for _, check := range checks {
			check()
		}
		if target.Visibility.VisibilityXID() != visibility || target.Visibility.LastAssignedXID() != assigned || target.Visibility.CommitSeq() != commit {
			t.Fatal("failed retirement changed selected visibility frontiers")
		}
		for resource, hold := range peerLocks {
			if current := state.Resources[resource]; current == nil || current.Holders[peerOwner.Advisory.Ticket] != hold {
				t.Fatal("failed DROP consumed peer lock ownership")
			}
		}
	}
}

func databaseDropCancellationOutput(t *testing.T, result core.Result) func() {
	t.Helper()
	rows := result.Rows.(*exec.FinalRows)
	if rows.Source != nil || rows.Count != 1 || len(rows.Values) != 1 || len(rows.Columns) != 1 {
		t.Fatal("fixture did not retain finalized detached output")
	}
	value, column := &rows.Values[0], &rows.Columns[0]
	typ := rows.Values[0].Type()
	if n, err := value.Integer(); err != nil || n != 11 || typ.ID() != sqltype.Int4 {
		t.Fatal("fixture output lacks typed committed value", n, err)
	}
	return func() {
		t.Helper()
		if rows.Source != nil || len(rows.Values) != 1 || len(rows.Columns) != 1 || &rows.Values[0] != value || &rows.Columns[0] != column || rows.Values[0].Type() != typ {
			t.Fatal("retirement mutated/rebuilt detached output backing or descriptor")
		}
		if got := enginetest.Tuples(result); len(got) != 1 || len(got[0]) != 1 || got[0][0] != int64(11) {
			t.Fatal("retirement changed completed output", got)
		}
	}
}
