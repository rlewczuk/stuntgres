package core_test

import (
	"errors"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestPreparedFoldProtocolPrivateContext(t *testing.T) {
	for _, phase := range []string{"unrelated", "rollback_unrelated", "target_definition"} {
		t.Run(phase, func(t *testing.T) {
			e := rootengine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE fold_commands(id int,v int)")
			enginetest.SessionSQL(t, s, "INSERT INTO fold_commands VALUES(1,0)")
			enginetest.SessionSQL(t, s, "SET extra_float_digits=3")
			enginetest.SessionSQL(t, s, "BEGIN")
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
			command(func(scope *core.StatementScope) error {
				_, err := scope.ParseProtocol("private", "UPDATE fold_commands SET v=length((point '0.10000000000000002,1')::text)", nil)
				return err
			})
			command(func(scope *core.StatementScope) error {
				_, err := scope.BindProtocolPortal("private", "private", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
				return err
			})
			registry := core.PayloadOf(s).PreparedSQL
			portal := registry.Binding("private")
			analysis := portal.Entry.Description.Analysis
			if analysis.Private != nil {
				t.Fatal("fixture did not Bind before lazy catalog journal creation")
			}
			owner := registry.Lookup("private").Description.Folds
			entries, generation := owner.Current().Entries, owner.Current().Generation
			enginetest.SessionSQL(t, s, "SAVEPOINT before_catalog")
			if phase == "target_definition" {
				enginetest.SessionSQL(t, s, "ALTER TABLE fold_commands ADD COLUMN extra int")
			} else {
				enginetest.SessionSQL(t, s, "CREATE TABLE fold_unrelated(v int)")
			}
			if s.Transactions.Catalog == nil {
				t.Fatal("fixture did not create private catalog context")
			}
			if phase == "rollback_unrelated" {
				enginetest.SessionSQL(t, s, "ROLLBACK TO before_catalog")
			}
			enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
			err := preparedFoldProtocolTry(t, e, s, cycle, false, func(scope *core.StatementScope) error {
				_, err := scope.ExecuteProtocolPortal("private", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
				return err
			})
			if phase == "target_definition" {
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("changed executable definition escaped portal fence", err)
				}
				enginetest.SessionSQL(t, s, "ROLLBACK")
				preparedFoldProtocolRows(t, s, "0")
			} else {
				if err != nil {
					t.Fatal("same-transaction journal replaced accepted portal", err)
				}
				preparedFoldProtocolRows(t, s, "23")
			}
			if portal.Entry.Description.Analysis != analysis || analysis.Private != nil || owner.Current().Generation != generation || &owner.Current().Entries[0] != &entries[0] {
				t.Fatal("portal Execute published current context into Bind or definition")
			}
			enginetest.SessionSQL(t, s, "ROLLBACK")
		})
	}
}
