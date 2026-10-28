package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func databaseSettingsStatement(e *core.Engine, value string) *parser.AlterDatabaseSettings {
	db, _ := e.CatalogRead().Database(e.Catalog.DatabaseAddress())
	return &parser.AlterDatabaseSettings{Database: parser.Identifier{Text: db.Name()}, Set: &parser.Set{Name: parser.Identifier{Text: "application_name"}, Values: []string{value}}}
}

func databaseSettingsScope(op *session.Operation, txOwner *session.TransactionOwner) *core.StatementScope {
	return &core.StatementScope{Op: op, Owner: txOwner, TxRef: txOwner.Ref, LockTicket: txOwner.Ticket}
}

func TestDatabaseSettingsPublication(t *testing.T) {
	for _, ownerChanged := range []bool{false, true} {
		t.Run(map[bool]string{false: "disjoint_merge", true: "owner_commit_fence"}[ownerChanged], func(t *testing.T) {
			e := engine.New()
			own, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			tx := enginetest.TransactionBegin(t, own)
			op := enginetest.TransactionOperation(t, own)
			key := access.RoleSettingKey{Database: e.Catalog.DatabaseAddress()}
			before := e.RoleStore.Published
			_, err := rolecmds.ExecuteDatabaseSettings(e, databaseSettingsScope(op, own.Transactions), databaseSettingsStatement(e, "private"), 0)
			op.Done()
			if err != nil {
				t.Fatal(err)
			}
			candidate := own.Transactions.Catalog.Roles
			if candidate == before || e.RoleStore.Published != before || candidate.Settings.Lookup(key)[0].Value != "private" || before.Settings.Lookup(key) != nil {
				t.Fatal("private row leaked or mutated committed backing")
			}
			committed := installSettingsUnit(t, e, peer, access.RoleSettingKey{}, "peer")
			if ownerChanged {
				successor := e.Roles().LookupRef("pg_read_all_data")
				e.Mu.Lock()
				next := catalog.CloneState(e.Catalog.State)
				db := *next.MustDatabase(key.Database)
				db.Owner = successor
				next.SetDescriptor(key.Database, &db)
				e.Catalog.PublishLocked(next)
				e.Mu.Unlock()
			}
			op = enginetest.TransactionOperation(t, own)
			outcome, err := e.Commit(op, tx)
			op.Done()
			if ownerChanged {
				if err == nil || outcome.Committed || e.RoleStore.Published != committed || candidate.Settings.Lookup(key)[0].Value != "private" {
					t.Fatal("stale database owner published private defaults", err)
				}
			} else {
				final := e.RoleStore.Published
				if err != nil || !outcome.Committed || final.Settings.Lookup(key)[0].Value != "private" || final.Settings.Lookup(access.RoleSettingKey{})[0].Value != "peer" || &final.Settings.Lookup(key)[0] != &candidate.Settings.Lookup(key)[0] {
					t.Fatal("disjoint commit lost row/backing", err)
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
