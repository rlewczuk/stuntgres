package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func installSettingsUnit(t *testing.T, e *core.Engine, s *session.Session, key access.RoleSettingKey, value string) *access.RoleRecord {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	base := e.OperationRolesLocked(op)
	next := base.WithSettings(base.Settings.WithRow(key, []access.RoleSetting{{Name: "application_name", Value: value}}))
	if err := core.InstallOperationRolesLocked(e, op, next); err != nil {
		t.Fatal(err)
	}
	return next
}

func TestRoleSettingsPublication(t *testing.T) {
	for _, peerGraph := range []bool{false, true} {
		t.Run(map[bool]string{false: "settings_peer", true: "graph_peer"}[peerGraph], func(t *testing.T) {
			e := engine.New()
			own, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			original := e.RoleStore.Published
			a := access.RoleSettingKey{}
			b := access.RoleSettingKey{Database: e.Catalog.DatabaseAddress()}
			tx := enginetest.TransactionBegin(t, own)
			first := installSettingsUnit(t, e, own, a, "first")
			mark := own.Transactions.Catalog
			enginetest.SessionSQL(t, own, "SAVEPOINT settings_mark")
			later := installSettingsUnit(t, e, own, a, "later")
			if mark.Roles != first || own.Transactions.Catalog.Roles != later || mark.RolesBase != original || e.RoleStore.Published != original {
				t.Fatal("journal COW/publication boundary")
			}
			enginetest.SessionSQL(t, own, "ROLLBACK TO settings_mark")
			if own.Transactions.Catalog.Roles != first || first.Settings.Lookup(a)[0].Value != "first" || later.Settings.Lookup(a)[0].Value != "later" {
				t.Fatal("savepoint lost immutable settings root")
			}
			peerSettings := installSettingsUnit(t, e, peer, b, "peer")
			if peerGraph {
				op := enginetest.TransactionOperation(t, peer)
				e.Mu.Lock()
				base := e.OperationRolesLocked(op)
				d, _ := base.Root.Read(authorization.PrincipalRef{}).Lookup("postgres")
				d.Comment = "peer graph"
				root, err := base.Root.PutDescriptor(d)
				if err == nil {
					err = core.InstallOperationRolesLocked(e, op, base.WithRoot(root))
				}
				e.Mu.Unlock()
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
			}
			// Read refresh must merge settings-only deltas even if graph lineage is
			// unchanged, without modifying the saved journal or its borrowed row.
			op := enginetest.TransactionOperation(t, own)
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			e.Mu.Unlock()
			op.Done()
			if err != nil || auth.RoleRecord.Settings.Lookup(a)[0].Value != "first" || auth.RoleRecord.Settings.Lookup(b)[0].Value != "peer" {
				t.Fatal("complete-record read refresh", err)
			}
			if mark.Roles != first || first.Settings.Lookup(b) != nil {
				t.Fatal("refresh mutated savepoint authority")
			}
			if !enginetest.TransactionEnd(t, own, tx, true).Committed {
				t.Fatal("settings transaction did not commit")
			}
			final := e.RoleStore.Published
			if final.Settings.Lookup(a)[0].Value != "first" || final.Settings.Lookup(b)[0].Value != "peer" || final.Revision <= peerSettings.Revision || &final.Settings.Lookup(a)[0] != &first.Settings.Lookup(a)[0] {
				t.Fatal("settings-only final fast path lost row/backing/revision")
			}
			if peerGraph {
				d, _ := final.Root.Read(authorization.PrincipalRef{}).Lookup("postgres")
				if d.Comment != "peer graph" {
					t.Fatal("settings merge overwrote graph peer")
				}
			}
		})
	}
}

func TestRoleSettingsPublicationConflict(t *testing.T) {
	e := engine.New()
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	key := access.RoleSettingKey{}
	tx := enginetest.TransactionBegin(t, a)
	candidate := installSettingsUnit(t, e, a, key, "private")
	committed := installSettingsUnit(t, e, b, key, "committed")
	op := enginetest.TransactionOperation(t, a)
	result, err := e.Commit(op, tx)
	op.Done()
	if err == nil || result.Committed || e.RoleStore.Published != committed || candidate.Settings.Lookup(key)[0].Value != "private" {
		t.Fatal("conflicting row published or mutated retained candidate", err)
	}
}

func TestRoleSettingsPublicationMixedGraph(t *testing.T) {
	e := engine.New()
	own, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	a, b := access.RoleSettingKey{}, access.RoleSettingKey{Database: e.Catalog.DatabaseAddress()}
	tx := enginetest.TransactionBegin(t, own)
	first := installSettingsUnit(t, e, own, a, "private")
	op := enginetest.TransactionOperation(t, own)
	e.Mu.Lock()
	d, _ := first.Root.Read(authorization.PrincipalRef{}).Lookup("postgres")
	d.Comment = "private graph"
	root, err := first.Root.PutDescriptor(d)
	if err == nil {
		err = core.InstallOperationRolesLocked(e, op, first.WithRoot(root))
	}
	if err == nil {
		// The actual ACL publication path advances this same complete record.
		journal := own.Transactions.Catalog.Clone()
		journal.Roles = journal.Roles.Advance()
		own.Transactions.Catalog = journal
	}
	saved := own.Transactions.Catalog
	e.Mu.Unlock()
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	peerRoot := installSettingsUnit(t, e, peer, b, "peer")
	if !enginetest.TransactionEnd(t, own, tx, true).Committed {
		t.Fatal("mixed graph/settings journal rolled back")
	}
	final := e.RoleStore.Published
	actual, _ := final.Root.Read(authorization.PrincipalRef{}).Lookup("postgres")
	if actual.Comment != "private graph" || final.Settings.Lookup(a)[0].Value != "private" || final.Settings.Lookup(b)[0].Value != "peer" || final.Revision <= peerRoot.Revision || saved.Roles.Settings != first.Settings || first.Settings.Lookup(b) != nil {
		t.Fatal("mixed graph/settings/ACL publication lost complete authority")
	}
}
