package core_test

import (
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/core"
)

func TestPreparedFoldProtocolCommandParameters(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE fold_commands(id int,v int)")
	enginetest.SessionSQL(t, s, "SET extra_float_digits=3")
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("parameterized", "INSERT INTO fold_commands VALUES($1,length((point '0.10000000000000002,1')::text))", nil)
		return err
	})
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "parameterized", &protocolUnitInput{data: []byte{0, 0, 0, 1, 0, 0, 0, 1, '2', 0, 0}})
			return err
		})
	}
	bind("old")
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("parameterized")
	if definition.Description.Folds != nil && len(definition.Description.Folds.Current().Entries) != 0 {
		t.Fatal("parameterized Bind published definition artifacts")
	}
	enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
	bind("new")
	old, new := registry.Binding("old"), registry.Binding("new")
	if old.Folds == nil || new.Folds == nil || len(old.Folds.Entries) == 0 || len(new.Folds.Entries) == 0 || &old.Folds.Entries[0] == &new.Folds.Entries[0] {
		t.Fatal("parameterized portals borrowed persistent definition backing")
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("parameterized") })
	command(func(scope *core.StatementScope) error {
		_, err := scope.ExecuteProtocolPortal("old", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	preparedFoldProtocolRows(t, s, "23")
	enginetest.SessionSQL(t, s, "DELETE FROM fold_commands")
	command(func(scope *core.StatementScope) error {
		_, err := scope.ExecuteProtocolPortal("new", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	preparedFoldProtocolRows(t, s, "7")
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
